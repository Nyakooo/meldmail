package storage

import (
	"context"
	"errors"
	"fmt"
)

type EmailFlags struct {
	ID        int64  `json:"id"`
	UID       uint32 `json:"-"`
	IsRead    bool   `json:"is_read"`
	IsStarred bool   `json:"is_starred"`
	Deleted   bool   `json:"deleted,omitempty"`
}

func (s *Store) FlagBatch(ctx context.Context, folderID int64, after uint32) (batch []EmailFlags, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,uid,is_read,is_starred FROM emails WHERE folder_id=? AND uid>? ORDER BY uid LIMIT 100", folderID, after)
	if err != nil {
		return nil, fmt.Errorf("storage flag batch: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	for rows.Next() {
		var f EmailFlags
		if err := rows.Scan(&f.ID, &f.UID, &f.IsRead, &f.IsStarred); err != nil {
			return nil, fmt.Errorf("storage scan flags: %w", err)
		}
		batch = append(batch, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage flag rows: %w", err)
	}
	return batch, nil
}
func (s *Store) ApplyRemoteFlags(ctx context.Context, folderID int64, flags EmailFlags) error {
	var err error
	if flags.Deleted {
		_, err = s.DB.ExecContext(ctx, "DELETE FROM emails WHERE id=? AND folder_id=?", flags.ID, folderID)
	} else {
		_, err = s.DB.ExecContext(ctx, "UPDATE emails SET is_read=?,is_starred=? WHERE id=? AND folder_id=?", flags.IsRead, flags.IsStarred, flags.ID, folderID)
	}
	if err != nil {
		return fmt.Errorf("storage apply flags: %w", err)
	}
	return nil
}
func (s *Store) RefreshUnread(ctx context.Context, folderID int64) error {
	if _, err := s.DB.ExecContext(ctx, "UPDATE folders SET unread_count=(SELECT COUNT(*) FROM emails WHERE folder_id=? AND is_read=0) WHERE id=?", folderID, folderID); err != nil {
		return fmt.Errorf("storage unread count: %w", err)
	}
	return nil
}
