package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) RemoveTrashMetadata(ctx context.Context, accountID int64, name string, validity, uid, upper uint32) (resultErr error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage remove trash transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if uid != 0 {
		where := `uid=? AND folder_id IN (SELECT id FROM folders WHERE account_id=? AND name=? AND role='trash' AND uid_validity=?)`
		if err := deleteTrashWhere(ctx, tx, where, accountID, name, uid, accountID, name, validity); err != nil {
			return err
		}
	} else {
		where := `folder_id IN (SELECT id FROM folders WHERE account_id=? AND name=? AND role='trash' AND (uid_validity<>? OR emails.uid<=?))`
		if err := deleteTrashWhere(ctx, tx, where, accountID, name, accountID, name, validity, upper); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage remove trash commit: %w", err)
	}
	return nil
}

func (s *Store) RemoveTrashMetadataBatch(ctx context.Context, accountID int64, name string, validity uint32, uids []uint32) (resultErr error) {
	if len(uids) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage remove trash batch transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	const chunkSize = 200
	for start := 0; start < len(uids); start += chunkSize {
		end := start + chunkSize
		if end > len(uids) {
			end = len(uids)
		}
		chunk := uids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]interface{}, 0, len(chunk)+3)
		for _, uid := range chunk {
			args = append(args, uid)
		}
		args = append(args, accountID, name, validity)
		where := `uid IN (` + placeholders + `) AND folder_id IN (SELECT id FROM folders WHERE account_id=? AND name=? AND role='trash' AND uid_validity=?)`
		if err := deleteTrashWhere(ctx, tx, where, accountID, name, args...); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage remove trash batch commit: %w", err)
	}
	return nil
}

func deleteTrashWhere(ctx context.Context, tx *sql.Tx, where string, accountID int64, name string, args ...interface{}) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN (SELECT '`+flagKeyPrefix+`'||id FROM emails WHERE `+where+`)`, args...); err != nil {
		return fmt.Errorf("storage remove trash pending flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM emails WHERE `+where, args...); err != nil {
		return fmt.Errorf("storage remove trash message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE folders SET unread_count=(SELECT COUNT(*) FROM emails WHERE folder_id=folders.id AND is_read=0)
		WHERE account_id=? AND name=? AND role='trash'`, accountID, name); err != nil {
		return fmt.Errorf("storage recount trash: %w", err)
	}
	return nil
}
