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
	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/proxy"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Service) ChangeFlags(ctx context.Context, id int64, change storage.FlagChange) (storage.Email, error) {
	e, err := s.Store.QueueFlags(ctx, id, change)
	if err != nil {
		return e, err
	}
	if err := s.publishPendingFlags([]storage.Email{e}, change); err != nil {
		return e, err
	}
	s.wakeFlags()
	return e, nil
}

func (s *Service) ChangeFlagsBatch(ctx context.Context, ids []int64, change storage.FlagChange) ([]storage.Email, error) {
	emails, err := s.Store.QueueFlagsBatch(ctx, ids, change)
	if err != nil {
		return nil, err
	}
	if err := s.publishPendingFlags(emails, change); err != nil {
		return emails, err
	}
	s.wakeFlags()
	return emails, nil
}

func pendingFlagPayload(e storage.Email, change storage.FlagChange) map[string]interface{} {
	return map[string]interface{}{
		"id":         e.ID,
		"is_read":    e.IsRead,
		"is_starred": e.IsStarred,
		"archived":   change.Archive,
		"deleted":    change.Delete,
		"restored":   change.Restore,
		"pending":    true,
	}
}

func (s *Service) publishPendingFlags(emails []storage.Email, change storage.FlagChange) error {
	if len(emails) == 0 {
		return nil
	}
	if len(emails) == 1 {
		return s.Events.Publish("flag_updated", pendingFlagPayload(emails[0], change))
	}
	const chunkSize = 50
	for start := 0; start < len(emails); start += chunkSize {
		end := start + chunkSize
		if end > len(emails) {
			end = len(emails)
		}
		updates := make([]map[string]interface{}, 0, end-start)
		for _, e := range emails[start:end] {
			updates = append(updates, pendingFlagPayload(e, change))
		}
		if err := s.Events.Publish("flag_updated", map[string]interface{}{"updates": updates}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) publishCompletedFlags(events []flagEvent) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) == 1 {
		return s.Events.Publish("flag_updated", events[0])
	}
	const chunkSize = 50
	for start := 0; start < len(events); start += chunkSize {
		end := start + chunkSize
		if end > len(events) {
			end = len(events)
		}
		if err := s.Events.Publish("flag_updated", map[string]interface{}{"updates": events[start:end]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RunBackground(ctx context.Context) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 3)
	go func() { done <- s.Workers.Run(child) }()
	go func() { done <- s.runFlagActions(child) }()
	go func() { s.runBodyPrefetch(child); done <- nil }()
	first := <-done
	cancel()
	return errors.Join(first, <-done, <-done)
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
func (s *Service) wakeFlags() {
	select {
	case s.flagWake <- struct{}{}:
	default:
	}
}

func (s *Service) ProcessFlagActions(ctx context.Context) error {
	for {
		recheck := false
		actions, err := s.Store.FlagActions(ctx)
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		var accountOrder []int64
		byAccount := make(map[int64][]storage.FlagAction)
		for _, action := range actions {
			if action.NextAt > now {
				continue
			}
			if _, ok := byAccount[action.AccountID]; !ok {
				accountOrder = append(accountOrder, action.AccountID)
			}
			byAccount[action.AccountID] = append(byAccount[action.AccountID], action)
		}
		for _, accountID := range accountOrder {
			if err := s.processAccountFlagActions(ctx, accountID, byAccount[accountID], &recheck); err != nil {
				return err
			}
		}
		if !recheck || ctx.Err() != nil {
			return nil
		}
	}
}

type pureFlagKey struct {
	folderID int64
	read     int8
	starred  int8
}

func makePureFlagKey(action storage.FlagAction) pureFlagKey {
	tri := func(v *bool) int8 {
		if v == nil {
			return -1
		}
		if *v {
			return 1
		}
		return 0
	}
	return pureFlagKey{folderID: action.Folder.ID, read: tri(action.Change.IsRead), starred: tri(action.Change.IsStarred)}
}

func (s *Service) processAccountFlagActions(ctx context.Context, accountID int64, group []storage.FlagAction, recheck *bool) error {
	timeout := 45*time.Second + time.Duration(len(group))*2*time.Second
	if timeout > 180*time.Second {
		timeout = 180 * time.Second
	}
	batchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	release, err := s.Limiter.Acquire(batchCtx, accountID)
	if err != nil {
		if errors.Is(err, syncer.ErrBusy) {
			return nil
		}
		for _, action := range group {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		return nil
	}
	defer release()
	a, err := s.Store.Account(batchCtx, accountID)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, action := range group {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		return nil
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		for _, action := range group {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		return nil
	}
	d, err := s.Dialer(batchCtx, a)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, action := range group {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		return nil
	}
	client, err := imapclient.Open(batchCtx, a, credentials, d, s.TLSConfig)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		for _, action := range group {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		return nil
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			slog.Default().LogAttrs(ctx, slog.LevelWarn, "mail flag batch close failed", append([]slog.Attr{slog.Int64("account_id", accountID)}, logging.Failure(closeErr)...)...)
		}
	}()

	var pureKeys []pureFlagKey
	pureBatches := make(map[pureFlagKey][]storage.FlagAction)
	var moveActions []storage.FlagAction
	for _, action := range group {
		if !action.MoveStarted && !action.Change.HasMove() {
			k := makePureFlagKey(action)
			if _, ok := pureBatches[k]; !ok {
				pureKeys = append(pureKeys, k)
			}
			pureBatches[k] = append(pureBatches[k], action)
		} else {
			moveActions = append(moveActions, action)
		}
	}

	var completedEvents []flagEvent
	defer func() {
		if pubErr := s.publishCompletedFlags(completedEvents); pubErr != nil {
			slog.Default().LogAttrs(ctx, slog.LevelWarn, "mail flag batch SSE publish failed", append([]slog.Attr{slog.Int64("account_id", accountID)}, logging.Failure(pubErr)...)...)
		}
	}()

	transportBroken := false
	for _, k := range pureKeys {
		sub := pureBatches[k]
		uids := make([]uint32, 0, len(sub))
		for _, action := range sub {
			uids = append(uids, action.UID)
		}
		err := client.ApplyFlagsBatch(batchCtx, sub[0].Folder, uids, sub[0].Change)
		if err == nil {
			for _, action := range sub {
				if doneErr := s.completeFlagSuccess(ctx, action, nil, 0, &completedEvents, recheck); doneErr != nil {
					return doneErr
				}
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, imapclient.ErrMessageGone) {
			for _, action := range sub {
				if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
					return failErr
				}
			}
			continue
		}
		for _, action := range sub {
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
		}
		transportBroken = true
		break
	}

	if !transportBroken {
		for _, action := range moveActions {
			destination, uid, err := s.applyActionWithClient(batchCtx, a, credentials, d, client, &action)
			if err == nil {
				if doneErr := s.completeFlagSuccess(ctx, action, destination, uid, &completedEvents, recheck); doneErr != nil {
					return doneErr
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if failErr := s.recordFlagFailure(ctx, action, err, recheck); failErr != nil {
				return failErr
			}
			if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, imapclient.ErrMessageGone) {
				break
			}
		}
	}
	return nil
}

func (s *Service) completeFlagSuccess(ctx context.Context, action storage.FlagAction, destination *storage.Folder, uid uint32, events *[]flagEvent, recheck *bool) error {
	pending, err := s.Store.CompleteFlagAction(ctx, action, destination, uid, false)
	if err != nil {
		return err
	}
	if pending {
		s.wakeFlags()
		*recheck = true
		return nil
	}
	e, err := s.Store.Email(ctx, action.EmailID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	*events = append(*events, flagEvent{Email: e, Archived: action.Change.Archive, Deleted: action.Change.Delete, Restored: action.Change.Restore, Pending: false})
	return nil
}

func (s *Service) recordFlagFailure(ctx context.Context, action storage.FlagAction, err error, recheck *bool) error {
	action.Attempts++
	if (action.Attempts >= 5 && !action.MoveStarted) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, imapclient.ErrMessageGone) {
		pending, finishErr := s.Store.CompleteFlagAction(ctx, action, nil, 0, true)
		if finishErr != nil {
			return finishErr
		}
		if pending {
			s.wakeFlags()
			*recheck = true
		} else {
			e, readErr := s.Store.Email(ctx, action.EmailID)
			if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
				return readErr
			}
			if readErr == nil {
				if pubErr := s.Events.Publish("new_email", e); pubErr != nil {
					return pubErr
				}
			}
		}
	} else {
		delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
		index := action.Attempts - 1
		if index >= len(delays) {
			index = len(delays) - 1
		}
		action.NextAt = time.Now().Add(delays[index]).UnixMilli()
		if retryErr := s.Store.RetryFlagAction(ctx, action); retryErr != nil {
			return retryErr
		}
	}
	attrs := []slog.Attr{slog.Int64("account_id", action.AccountID), slog.String("mode", "flag_retry")}
	slog.Default().LogAttrs(ctx, slog.LevelWarn, "mail flag action failed", append(attrs, logging.Failure(err)...)...)
	return nil
}

func (s *Service) applyActionWithClient(ctx context.Context, a storage.Account, credentials auth.Credentials, d *proxy.Dialer, client *imapclient.Client, action *storage.FlagAction) (*storage.Folder, uint32, error) {
	var moved *imapclient.MoveResult
	var err error
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
