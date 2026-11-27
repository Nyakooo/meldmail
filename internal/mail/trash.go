package mail

import (
	"context"
	"database/sql"
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

func (s *Service) PurgeEmails(ctx context.Context, ids []int64) error {
	unique, ok := storage.UniquePositiveIDs(ids, 100)
	if !ok {
		return storage.ErrInvalidQuery
	}
	type folderBatch struct {
		folder   storage.Folder
		uids     []uint32
		emailIDs []int64
	}
	type accountBatch struct {
		accountID   int64
		folderOrder []int64
		folders     map[int64]*folderBatch
	}
	var accountOrder []int64
	byAccount := make(map[int64]*accountBatch)
	targetSet := make(map[int64]struct{}, len(unique))
	for _, id := range unique {
		email, _, folder, err := s.emailSource(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if folder.Role != "trash" || strings.EqualFold(folder.Name, "INBOX") {
			return storage.ErrInvalidQuery
		}
		targetSet[id] = struct{}{}
		ab := byAccount[email.AccountID]
		if ab == nil {
			ab = &accountBatch{accountID: email.AccountID, folders: make(map[int64]*folderBatch)}
			byAccount[email.AccountID] = ab
			accountOrder = append(accountOrder, email.AccountID)
		}
		fb := ab.folders[folder.ID]
		if fb == nil {
			fb = &folderBatch{folder: folder}
			ab.folders[folder.ID] = fb
			ab.folderOrder = append(ab.folderOrder, folder.ID)
		}
		fb.uids = append(fb.uids, email.UID)
		fb.emailIDs = append(fb.emailIDs, email.ID)
	}
	if len(targetSet) == 0 {
		return sql.ErrNoRows
	}
	actions, err := s.Store.FlagActions(ctx)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if !action.BlocksPurge() {
			continue
		}
		if _, targeted := targetSet[action.EmailID]; targeted {
			return storage.ErrActionPending
		}
	}
	for _, accountID := range accountOrder {
		ab := byAccount[accountID]
		if err := s.purgeAccountTrashBatch(ctx, accountID, ab.folderOrder, func(fid int64) (storage.Folder, []uint32, []int64) {
			fb := ab.folders[fid]
			return fb.folder, fb.uids, fb.emailIDs
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) purgeAccountTrashBatch(ctx context.Context, accountID int64, folderOrder []int64, lookup func(int64) (storage.Folder, []uint32, []int64)) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	release, err := s.Limiter.Acquire(ctx, accountID)
	if err != nil {
		return err
	}
	defer release()
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
	for _, folderID := range folderOrder {
		folder, uids, emailIDs := lookup(folderID)
		validity, err := client.PurgeTrashBatch(ctx, folder.Name, folder.UIDValidity, uids)
		if err != nil {
			return err
		}
		if err := s.Store.RemoveTrashMetadataBatch(ctx, accountID, folder.Name, validity, uids); err != nil {
			return err
		}
		payload := map[string]interface{}{"account_id": accountID, "email_id": int64(0), "email_ids": emailIDs}
		if len(emailIDs) == 1 {
			payload = map[string]interface{}{"account_id": accountID, "email_id": emailIDs[0]}
		}
		if err := s.Events.Publish("trash_changed", payload); err != nil {
			return err
		}
	}
	return nil
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
		if !action.BlocksPurge() {
			continue
		}
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
