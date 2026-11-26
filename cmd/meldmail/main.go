package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyakooo/meldmail/internal/api"
	"github.com/nyakooo/meldmail/internal/config"
	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func run() (result error) {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	files, err := logging.Open(c.LogDir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stdout, files), nil))
	slog.SetDefault(logger)
	logDone := make(chan error, 1)
	logCtx, cancelLogs := context.WithCancel(context.Background())
	go func() { logDone <- files.Maintain(logCtx) }()
	defer func() {
		if result != nil {
			logger.LogAttrs(context.Background(), slog.LevelError, "application stopped", logging.Failure(result)...)
		} else {
			logger.Info("application stopped")
		}
		cancelLogs()
		if err := <-logDone; err != nil {
			result = errors.Join(result, err)
		}
		if err := files.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	logger.Info("application starting", "log_retention_days", 7)
	store, err := storage.Open(ctx, c.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close database: %w", err))
		}
	}()
	if c.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(c.AdminPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash administrator password: %w", err)
		}
		if err := store.InitPassword(ctx, string(hash)); err != nil {
			return err
		}
	} else if _, err := store.Setting(ctx, "admin_password"); err != nil {
		return fmt.Errorf("configure MELDMAIL_ADMIN_PASSWORD for first startup: %w", err)
	}
	mailService, err := mail.New(store, c.MasterKey)
	if err != nil {
		return err
	}
	if err := mailService.ConfigureHostProxy(ctx, c.HostProxyURL); err != nil {
		return err
	}
	handler, err := api.New(store, c.TrustProxy, mailService, files)
	if err != nil {
		return err
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	mailService.Workers = mailService.NewWorkers(syncer.DefaultOptions())
	workerDone := make(chan error, 1)
	go func() { workerDone <- mailService.RunBackground(runCtx) }()
	workerExited := false
	defer func() {
		cancelRun()
		if !workerExited {
			if err := <-workerDone; err != nil {
				result = errors.Join(result, err)
			}
		}
	}()
	server := &http.Server{Addr: c.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, BaseContext: func(net.Listener) context.Context { return runCtx }}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	serverExited := false
	select {
	case err := <-done:
		serverExited = true
		if !errors.Is(err, http.ErrServerClosed) {
			result = fmt.Errorf("http listen: %w", err)
		}
	case err := <-workerDone:
		workerExited = true
		if ctx.Err() == nil {
			if err == nil {
				err = fmt.Errorf("unexpected worker exit")
			}
			result = fmt.Errorf("background workers stopped: %w", err)
		} else if err != nil && !errors.Is(err, context.Canceled) {
			result = fmt.Errorf("background worker shutdown: %w", err)
		}
	case <-ctx.Done():
	}
	cancelRun()
	if !serverExited {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			result = errors.Join(result, fmt.Errorf("http shutdown: %w", err))
			if err := server.Close(); err != nil {
				result = errors.Join(result, fmt.Errorf("http close: %w", err))
			}
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("http server: %w", err))
		}
	}
	return result
}
func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).LogAttrs(context.Background(), slog.LevelError, "application failed", logging.Failure(err)...)
		os.Exit(1)
	}
}
