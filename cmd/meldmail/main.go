package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyakooo/meldmail/internal/api"
	"github.com/nyakooo/meldmail/internal/config"
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
	handler, err := api.New(store, c.TrustProxy)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: c.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http listen: %w", err)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
