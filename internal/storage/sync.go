package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Folder struct {
	ID, AccountID        int64
	Name, Role           string
	UIDValidity, LastUID uint32
}
type Email struct {
	ID             int64  `json:"id"`
	FolderRole     string `json:"folder_role"`
	AccountID      int64  `json:"account_id"`
	FolderID       int64  `json:"folder_id"`
	UID            uint32 `json:"uid"`
	MessageID      string `json:"message_id"`
	InReplyTo      string `json:"in_reply_to"`
	Subject        string `json:"subject"`
	FromName       string `json:"from_name"`
	FromAddr       string `json:"from_addr"`
	ToAddrs        string `json:"to_addrs"`
	CCAddrs        string `json:"cc_addrs"`
	Snippet        string `json:"snippet"`
	HasAttachments bool   `json:"has_attachments"`
	IsRead         bool   `json:"is_read"`
	IsStarred      bool   `json:"is_starred"`
	Date           int64  `json:"date"`
	CreatedAt      int64  `json:"created_at"`
}

func (s *Store) PrepareFolder(ctx context.Context, accountID int64, name, role string, validity uint32) (result Folder, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Folder{}, fmt.Errorf("storage folder transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if e := tx.Rollback(); e != nil {
				err = errors.Join(err, fmt.Errorf("storage folder rollback: %w", e))
			}
		}
	}()
	if _, err = tx.ExecContext(ctx, `INSERT INTO folders(account_id,name,role,uid_validity) VALUES(?,?,?,?) ON CONFLICT(account_id,name) DO NOTHING`, accountID, name, role, validity); err != nil {
		return Folder{}, fmt.Errorf("storage insert folder: %w", err)
	}
	var f Folder
	f.AccountID = accountID
	f.Name = name
	f.Role = role
	if err = tx.QueryRowContext(ctx, "SELECT id,uid_validity,last_uid FROM folders WHERE account_id=? AND name=?", accountID, name).Scan(&f.ID, &f.UIDValidity, &f.LastUID); err != nil {
		return Folder{}, fmt.Errorf("storage read folder: %w", err)
	}
	if f.UIDValidity != validity {
		if _, err = tx.ExecContext(ctx, "DELETE FROM emails WHERE folder_id=?", f.ID); err != nil {
			return Folder{}, fmt.Errorf("storage reset emails: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE folders SET uid_validity=?,last_uid=0,unread_count=0 WHERE id=?", validity, f.ID); err != nil {
			return Folder{}, fmt.Errorf("storage reset folder: %w", err)
		}
		f.UIDValidity = validity
		f.LastUID = 0
	}
	if err = tx.Commit(); err != nil {
		return Folder{}, fmt.Errorf("storage commit folder: %w", err)
	}
	return f, nil
}
func (s *Store) SaveMetadata(ctx context.Context, f Folder, e Email) (result Email, err error) {
	// This is the only path for synced content: bounded metadata, never body/MIME/attachments.
	r := []rune(e.Snippet)
	if len(r) > 500 {
		e.Snippet = string(r[:500])
	}
	if len(e.Subject) > 8192 || len(e.FromName) > 2048 || len(e.FromAddr) > 1024 || len(e.ToAddrs) > 65536 || len(e.CCAddrs) > 65536 {
		return Email{}, fmt.Errorf("storage: oversized metadata")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Email{}, fmt.Errorf("storage metadata transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if e := tx.Rollback(); e != nil {
				err = errors.Join(err, fmt.Errorf("storage metadata rollback: %w", e))
			}
		}
	}()
	if e.UID == 0 {
		return Email{}, fmt.Errorf("storage: invalid UID")
	}
	e.AccountID = f.AccountID
	e.FolderID = f.ID
	e.CreatedAt = time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO emails(account_id,folder_id,uid,message_id,in_reply_to,subject,from_name,from_addr,to_addrs,cc_addrs,snippet,has_attachments,is_read,is_starred,date,created_at) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM folders WHERE id=? AND account_id=? AND uid_validity=?) ON CONFLICT(account_id,folder_id,uid) DO UPDATE SET is_read=CASE WHEN `+pendingFlagsSQL("emails.id")+` THEN emails.is_read ELSE excluded.is_read END,is_starred=CASE WHEN `+pendingFlagsSQL("emails.id")+` THEN emails.is_starred ELSE excluded.is_starred END`, e.AccountID, e.FolderID, e.UID, e.MessageID, e.InReplyTo, e.Subject, e.FromName, e.FromAddr, e.ToAddrs, e.CCAddrs, e.Snippet, e.HasAttachments, e.IsRead, e.IsStarred, e.Date, e.CreatedAt, f.ID, f.AccountID, f.UIDValidity); err != nil {
		return Email{}, fmt.Errorf("storage save metadata: %w", err)
	}
	e, err = scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails e JOIN folders f ON f.id=e.folder_id WHERE e.account_id=? AND e.folder_id=? AND e.uid=?", f.AccountID, f.ID, e.UID))
	if err != nil {
		return Email{}, fmt.Errorf("storage saved metadata: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE folders SET unread_count=(SELECT COUNT(*) FROM emails WHERE folder_id=? AND is_read=0) WHERE id=? AND uid_validity=?", f.ID, f.ID, f.UIDValidity); err != nil {
		return Email{}, fmt.Errorf("storage advance UID: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE accounts SET last_sync_at=?,last_error='',status='active' WHERE id=?", time.Now().Unix(), f.AccountID); err != nil {
		return Email{}, fmt.Errorf("storage sync timestamp: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Email{}, fmt.Errorf("storage commit metadata: %w", err)
	}
	return e, nil
}

// FinishSync advances the cursor only after the complete FETCH succeeds. Partial
// failures can therefore retry without skipping lower UIDs already in flight.
func (s *Store) FinishSync(ctx context.Context, f Folder, lastUID uint32) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE folders SET last_uid=MAX(last_uid,?) WHERE id=? AND account_id=? AND uid_validity=?", lastUID, f.ID, f.AccountID, f.UIDValidity)
	if err != nil {
		return fmt.Errorf("storage finish sync: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage sync affected rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("storage: folder changed during sync")
	}
	return nil
}
