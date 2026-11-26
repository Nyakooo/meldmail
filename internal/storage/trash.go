package storage

import (
	"context"
	"fmt"
)

func (s *Store) RemoveTrashMetadata(ctx context.Context, accountID int64, name string, validity, uid, upper uint32) error {
	if uid != 0 {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM emails WHERE uid=? AND folder_id IN
			(SELECT id FROM folders WHERE account_id=? AND name=? AND role='trash' AND uid_validity=?)`, uid, accountID, name, validity); err != nil {
			return fmt.Errorf("storage remove trash message: %w", err)
		}
	} else {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM emails WHERE folder_id IN
			(SELECT id FROM folders WHERE account_id=? AND name=? AND role='trash' AND (uid_validity<>? OR emails.uid<=?))`, accountID, name, validity, upper); err != nil {
			return fmt.Errorf("storage clear trash: %w", err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE folders SET unread_count=(SELECT COUNT(*) FROM emails WHERE folder_id=folders.id AND is_read=0)
		WHERE account_id=? AND name=? AND role='trash'`, accountID, name); err != nil {
		return fmt.Errorf("storage recount trash: %w", err)
	}
	return nil
}
