package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var ErrActionPending = errors.New("a flag action is already pending for this email")

type FlagChange struct {
	IsRead    *bool `json:"is_read,omitempty"`
	IsStarred *bool `json:"is_starred,omitempty"`
	Archive   bool  `json:"archive,omitempty"`
	Delete    bool  `json:"delete,omitempty"`
}

func (c FlagChange) Valid() bool {
	return !(c.Archive && c.Delete) && (c.IsRead != nil || c.IsStarred != nil || c.Archive || c.Delete)
}

type FlagAction struct {
	EmailID    int64      `json:"email_id"`
	AccountID  int64      `json:"account_id"`
	Folder     Folder     `json:"folder"`
	UID        uint32     `json:"uid"`
	MessageID  string     `json:"message_id"`
	Change     FlagChange `json:"change"`
	OldRead    bool       `json:"old_read"`
	OldStarred bool       `json:"old_starred"`
	Attempts   int        `json:"attempts"`
	NextAt     int64      `json:"next_at"`
}

func flagKey(id int64) string { return "flag_action:" + strconv.FormatInt(id, 10) }
func (s *Store) QueueFlags(ctx context.Context, id int64, change FlagChange) (updated Email, resultErr error) {
	if !change.Valid() {
		return Email{}, ErrInvalidQuery
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Email{}, fmt.Errorf("storage flag transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	e, err := scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails e WHERE e.id=?", id))
	if err != nil {
		return e, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key=?", flagKey(id)).Scan(&count); err != nil {
		return e, fmt.Errorf("storage pending flag: %w", err)
	}
	if count > 0 {
		return e, ErrActionPending
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key LIKE 'flag_action:%'").Scan(&count); err != nil {
		return e, fmt.Errorf("storage flag count: %w", err)
	}
	if count >= 128 {
		return e, fmt.Errorf("storage: flag queue full")
	}
	var f Folder
	if err := tx.QueryRowContext(ctx, "SELECT id,account_id,name,role,uid_validity,last_uid FROM folders WHERE id=?", e.FolderID).Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.UIDValidity, &f.LastUID); err != nil {
		return e, fmt.Errorf("storage flag folder: %w", err)
	}
	action := FlagAction{EmailID: id, AccountID: e.AccountID, Folder: f, UID: e.UID, MessageID: e.MessageID, Change: change, OldRead: e.IsRead, OldStarred: e.IsStarred}
	data, err := json.Marshal(action)
	if err != nil {
		return e, fmt.Errorf("storage flag JSON: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value,updated_at) VALUES(?,?,?)", flagKey(id), string(data), time.Now().Unix()); err != nil {
		return e, fmt.Errorf("storage enqueue flags: %w", err)
	}
	if change.IsRead != nil {
		e.IsRead = *change.IsRead
	}
	if change.IsStarred != nil {
		e.IsStarred = *change.IsStarred
	}
	if _, err := tx.ExecContext(ctx, "UPDATE emails SET is_read=?,is_starred=? WHERE id=?", e.IsRead, e.IsStarred, id); err != nil {
		return e, fmt.Errorf("storage optimistic flags: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return e, fmt.Errorf("storage commit flags: %w", err)
	}
	return e, nil
}
func (s *Store) FlagActions(ctx context.Context) (actions []FlagAction, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT value FROM settings WHERE key LIKE 'flag_action:%' ORDER BY updated_at,key LIMIT 128")
	if err != nil {
		return nil, fmt.Errorf("storage flag actions: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("storage flag action row: %w", err)
		}
		var action FlagAction
		if err := json.Unmarshal([]byte(value), &action); err != nil {
			return nil, fmt.Errorf("storage flag action decode: %w", err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage flag action rows: %w", err)
	}
	return actions, nil
}
func (s *Store) RetryFlagAction(ctx context.Context, action FlagAction) error {
	data, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("storage retry flags JSON: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE settings SET value=?,updated_at=? WHERE key=?", string(data), time.Now().Unix(), flagKey(action.EmailID)); err != nil {
		return fmt.Errorf("storage retry flags: %w", err)
	}
	return nil
}
func (s *Store) FinishFlagAction(ctx context.Context, action FlagAction, destination *Folder, uid uint32, rollback bool) (resultErr error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage finish flags transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if rollback {
		if _, err := tx.ExecContext(ctx, "UPDATE emails SET is_read=?,is_starred=? WHERE id=?", action.OldRead, action.OldStarred, action.EmailID); err != nil {
			return fmt.Errorf("storage rollback local flags: %w", err)
		}
	} else if destination != nil {
		if uid == 0 {
			return fmt.Errorf("storage: missing destination UID")
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM emails WHERE account_id=? AND folder_id=? AND uid=? AND id<>?", action.AccountID, destination.ID, uid, action.EmailID); err != nil {
			return fmt.Errorf("storage deduplicate moved email: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE emails SET folder_id=?,uid=? WHERE id=?", destination.ID, uid, action.EmailID); err != nil {
			return fmt.Errorf("storage relocate email: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM settings WHERE key=?", flagKey(action.EmailID)); err != nil {
		return fmt.Errorf("storage finish flags: %w", err)
	}
	for _, folderID := range []int64{action.Folder.ID, func() int64 {
		if destination != nil {
			return destination.ID
		}
		return action.Folder.ID
	}()} {
		if _, err := tx.ExecContext(ctx, "UPDATE folders SET unread_count=(SELECT COUNT(*) FROM emails WHERE folder_id=? AND is_read=0) WHERE id=?", folderID, folderID); err != nil {
			return fmt.Errorf("storage flag unread count: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage finish flags commit: %w", err)
	}
	return nil
}
