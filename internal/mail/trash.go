package mail

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Service) PurgeEmail(ctx context.Context, id int64) error {
	email, err := s.Store.Email(ctx, id)
	if err != nil {
		return err
	}
	return s.purgeTrash(ctx, email.AccountID, id)
}

func (s *Service) EmptyTrash(ctx context.Context, accountID int64) error {
	return s.purgeTrash(ctx, accountID, 0)
}

func (s *Service) purgeTrash(ctx context.Context, accountID, emailID int64) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	release, err := s.Limiter.Acquire(ctx, accountID)
	if err != nil {
		return err
	}
	defer release()
	var email storage.Email
	var folder storage.Folder
	if emailID != 0 {
		email, _, folder, err = s.emailSource(ctx, emailID)
		if err != nil {
			return err
		}
		if folder.Role != "trash" || strings.EqualFold(folder.Name, "INBOX") {
			return storage.ErrInvalidQuery
		}
	}
	actions, err := s.Store.FlagActions(ctx)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if action.EmailID == emailID || emailID == 0 && action.AccountID == accountID &&
			(action.Folder.Role == "trash" || action.Change.Delete) {
			return storage.ErrActionPending
		}
	}
	account, err := s.Store.Account(ctx, accountID)
	if err != nil {
		return err
	}
	credentials, err := s.Credentials(account)
	if err != nil {
		return err
	}
	dialer, err := s.Dialer(ctx, account)
	if err != nil {
		return err
	}
	client, err := imapclient.Open(ctx, account, credentials, dialer, s.TLSConfig)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	names := []string{folder.Name}
	if emailID == 0 {
		names, err = client.TrashMailboxes(ctx)
		if err != nil {
			return err
		}
	}
	for _, name := range names {
		validity, upper, err := client.PurgeTrash(ctx, name, folder.UIDValidity, email.UID)
		if err != nil {
			return err
		}
		if err := s.Store.RemoveTrashMetadata(ctx, accountID, name, validity, email.UID, upper); err != nil {
			return err
		}
		if err := s.Events.Publish("trash_changed", map[string]int64{"account_id": accountID, "email_id": emailID}); err != nil {
			return err
		}
	}
	return nil
}
