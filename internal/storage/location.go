package storage

import (
	"context"
	"fmt"
)

func (s *Store) Folder(ctx context.Context, id int64) (Folder, error) {
	var f Folder
	if err := s.DB.QueryRowContext(ctx, "SELECT id,account_id,name,role,uid_validity,last_uid FROM folders WHERE id=?", id).Scan(&f.ID, &f.AccountID, &f.Name, &f.Role, &f.UIDValidity, &f.LastUID); err != nil {
		return f, fmt.Errorf("storage folder: %w", err)
	}
	return f, nil
}
