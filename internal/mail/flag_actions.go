package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail/adapter"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Service) ChangeFlags(ctx context.Context, id int64, change storage.FlagChange) (storage.Email, error) {
	e, err := s.Store.QueueFlags(ctx, id, change)
	if err != nil {
		return e, err
	}
	if err := s.Events.Publish("flag_updated", map[string]interface{}{"id": e.ID, "is_read": e.IsRead, "is_starred": e.IsStarred, "archived": change.Archive, "deleted": change.Delete, "restored": change.Restore, "pending": true}); err != nil {
		return e, err
	}
	select {
	case s.flagWake <- struct{}{}:
	default:
	}
	return e, nil
}
func (s *Service) RunBackground(ctx context.Context) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- s.Workers.Run(child) }()
	go func() { done <- s.runFlagActions(child) }()
	first := <-done
	cancel()
	second := <-done
	return errors.Join(first, second)
}
func (s *Service) runFlagActions(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.ProcessFlagActions(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.flagWake:
		case <-ticker.C:
		}
	}
}
func (s *Service) ProcessFlagActions(ctx context.Context) error {
	actions, err := s.Store.FlagActions(ctx)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if action.NextAt > time.Now().UnixMilli() {
			continue
		}
		destination, uid, err := s.applyFlagAction(ctx, &action)
		if errors.Is(err, syncer.ErrBusy) {
			continue
		}
		if err == nil {
			if err := s.Store.FinishFlagAction(ctx, action, destination, uid, false); err != nil {
				return err
			}
			e, err := s.Store.Email(ctx, action.EmailID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := s.Events.Publish("flag_updated", flagEvent{Email: e, Archived: action.Change.Archive, Deleted: action.Change.Delete, Restored: action.Change.Restore, Pending: false}); err != nil {
				return err
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		action.Attempts++
		if (action.Attempts >= 5 && !action.MoveStarted) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, imapclient.ErrMessageGone) {
			if err := s.Store.FinishFlagAction(ctx, action, nil, 0, true); err != nil {
				return err
			}
			e, readErr := s.Store.Email(ctx, action.EmailID)
			if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
				return readErr
			}
			if readErr == nil {
				if err := s.Events.Publish("new_email", e); err != nil {
					return err
				}
			}
		} else {
			delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
			index := action.Attempts - 1
			if index >= len(delays) {
				index = len(delays) - 1
			}
			action.NextAt = time.Now().Add(delays[index]).UnixMilli()
			if err := s.Store.RetryFlagAction(ctx, action); err != nil {
				return err
			}
		}
		attrs := []slog.Attr{slog.Int64("account_id", action.AccountID), slog.String("mode", "flag_retry")}
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "mail flag action failed", append(attrs, logging.Failure(err)...)...)
	}
	return nil
}
func (s *Service) applyFlagAction(ctx context.Context, action *storage.FlagAction) (destination *storage.Folder, uid uint32, result error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	release, err := s.Limiter.Acquire(ctx, action.AccountID)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	a, err := s.Store.Account(ctx, action.AccountID)
	if err != nil {
		return nil, 0, err
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return nil, 0, err
	}
	d, err := s.Dialer(ctx, a)
	if err != nil {
		return nil, 0, err
	}
	client, err := imapclient.Open(ctx, a, credentials, d, s.TLSConfig)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	var moved *imapclient.MoveResult
	if action.MoveStarted && action.DestinationName != "" {
		moved, err = client.RecoverMove(ctx, *action)
		if err != nil {
			return nil, 0, err
		}
	}
	if moved == nil {
		moved, err = client.ApplyFlags(ctx, a, *action, func(checkpoint imapclient.MoveResult) error {
			action.MoveStarted = true
			action.MoveAcknowledged = checkpoint.Acknowledged
			action.DestinationName = checkpoint.Name
			action.DestinationRole = checkpoint.Role
			if checkpoint.UID != 0 {
				action.DestinationUID = checkpoint.UID
				action.DestinationValidity = checkpoint.UIDValidity
			}
			return s.Store.RetryFlagAction(ctx, *action)
		})
	}

	if err != nil {
		return nil, 0, err
	}
	if moved == nil {
		return nil, 0, nil
	}
	if a.Provider == "gmail" && action.Change.Archive {
		moved.UID, moved.UIDValidity, err = adapter.GmailArchive(ctx, a, credentials, d, s.TLSConfig, action.Folder, action.UID, moved.Name, action.MessageID, func(uid, validity uint32, acknowledged bool) error {
			action.MoveStarted = true
			action.MoveAcknowledged = acknowledged
			action.DestinationName = moved.Name
			action.DestinationRole = moved.Role
			action.DestinationUID = uid
			action.DestinationValidity = validity
			return s.Store.RetryFlagAction(ctx, *action)
		})
		if err != nil {
			return nil, 0, err
		}
	}
	f, err := s.Store.PrepareFolder(ctx, a.ID, moved.Name, moved.Role, moved.UIDValidity)
	if err != nil {
		return nil, 0, fmt.Errorf("flag destination cache: %w", err)
	}
	return &f, moved.UID, nil
}

type flagEvent struct {
	storage.Email
	Archived bool `json:"archived"`
	Deleted  bool `json:"deleted"`
	Restored bool `json:"restored"`
	Pending  bool `json:"pending"`
}
