package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const MaxCachedBodyBytes = 5 * 1024 * 1024
const MaxCachedAttachmentMetadataBytes = 256 * 1024
const BodyCacheBytes = 100 * 1024 * 1024
const BodyCacheEntries = 1000

type CachedBody struct {
	HTML, Text, AttachmentsJSON string
}

func (s *Store) Body(ctx context.Context, id int64, source string) (CachedBody, bool, error) {
	var body CachedBody
	err := s.DB.QueryRowContext(ctx, "SELECT html,text,attachments_json FROM email_bodies WHERE email_id=? AND source_key=?", id, source).Scan(&body.HTML, &body.Text, &body.AttachmentsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return body, false, nil
	}
	if err != nil {
		return body, false, fmt.Errorf("storage read cached body: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE email_bodies SET last_access=? WHERE email_id=? AND source_key=?", time.Now().UnixNano(), id, source); err != nil {
		return body, true, fmt.Errorf("storage touch cached body: %w", err)
	}
	return body, true, nil
}

func (s *Store) SaveBody(ctx context.Context, email Email, source string, body CachedBody) (resultErr error) {
	if len(body.HTML)+len(body.Text) > MaxCachedBodyBytes || len(body.AttachmentsJSON) > MaxCachedAttachmentMetadataBytes || source == "" || len(source) > 128 {
		return fmt.Errorf("storage cached body exceeds limits or has no source")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage begin cached body: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, fmt.Errorf("storage rollback cached body: %w", err))
			}
		}
	}()
	now := time.Now().UnixNano()
	// The message may have been removed or moved while IMAP was fetching it.
	result, err := tx.ExecContext(ctx, `INSERT INTO email_bodies(email_id,source_key,html,text,attachments_json,size_bytes,stored_at,last_access)
SELECT id,?,?,?,?,?,?,? FROM emails WHERE id=? AND account_id=? AND folder_id=? AND uid=?
ON CONFLICT(email_id) DO UPDATE SET source_key=excluded.source_key,html=excluded.html,text=excluded.text,attachments_json=excluded.attachments_json,size_bytes=excluded.size_bytes,stored_at=excluded.stored_at,last_access=excluded.last_access`,
		source, body.HTML, body.Text, body.AttachmentsJSON, len(body.HTML)+len(body.Text)+len(body.AttachmentsJSON), now, now, email.ID, email.AccountID, email.FolderID, email.UID)
	if err != nil {
		return fmt.Errorf("storage save cached body: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage cached body affected rows: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("storage cached body source changed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_bodies WHERE email_id IN (
SELECT email_id FROM (
 SELECT email_id,SUM(size_bytes) OVER (ORDER BY last_access DESC,email_id DESC) AS retained_bytes,
 ROW_NUMBER() OVER (ORDER BY last_access DESC,email_id DESC) AS retained_count FROM email_bodies
) WHERE retained_bytes>? OR retained_count>?)`, BodyCacheBytes, BodyCacheEntries); err != nil {
		return fmt.Errorf("storage trim cached bodies: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage commit cached body: %w", err)
	}
	return nil
}

func (s *Store) BodyPrefetchCandidates(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT recent.id FROM (
 SELECT e.id, e.date, a.proxy_mode <> 'direct' AS proxied
 FROM emails e JOIN accounts a ON a.id=e.account_id JOIN folders f ON f.id=e.folder_id
 WHERE e.is_read=0 AND f.role='inbox' AND a.status='active'
 ORDER BY proxied DESC, e.date DESC, e.id DESC LIMIT ?
) recent WHERE NOT EXISTS (SELECT 1 FROM email_bodies b WHERE b.email_id=recent.id)
ORDER BY recent.proxied DESC, recent.date DESC, recent.id DESC`, 16)
	if err != nil {
		return nil, fmt.Errorf("storage list body prefetch candidates: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage scan body prefetch candidate: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage iterate body prefetch candidates: %w", err)
	}
	return ids, nil
}
