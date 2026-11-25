package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrInvalidQuery = errors.New("invalid email query")

type EmailQuery struct {
	AccountID int64
	Folder    string
	Unread    bool
	Q, Cursor string
	Limit     int
}
type EmailPage struct {
	Total      int64   `json:"total"`
	Emails     []Email `json:"emails"`
	NextCursor string  `json:"next_cursor"`
}
type emailCursor struct{ Date, ID int64 }

const emailColumns = `e.id,e.account_id,e.folder_id,e.uid,e.message_id,e.in_reply_to,e.subject,e.from_name,e.from_addr,e.to_addrs,e.cc_addrs,e.snippet,e.has_attachments,e.is_read,e.is_starred,e.date,e.created_at,f.role`

func scanEmail(row scanner, extra ...interface{}) (Email, error) {
	var e Email
	fields := []interface{}{&e.ID, &e.AccountID, &e.FolderID, &e.UID, &e.MessageID, &e.InReplyTo, &e.Subject, &e.FromName, &e.FromAddr, &e.ToAddrs, &e.CCAddrs, &e.Snippet, &e.HasAttachments, &e.IsRead, &e.IsStarred, &e.Date, &e.CreatedAt, &e.FolderRole}
	err := row.Scan(append(fields, extra...)...)
	if err != nil {
		return e, fmt.Errorf("storage email: %w", err)
	}
	return e, nil
}
func (s *Store) ListEmails(ctx context.Context, q EmailQuery) (page EmailPage, resultErr error) {
	page.Emails = make([]Email, 0)
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 || q.AccountID < 0 || utf8.RuneCountInString(q.Q) > 256 || len(q.Cursor) > 256 {
		return page, ErrInvalidQuery
	}
	deleted := pendingRemovalSQL("e.id", false)
	archived := pendingRemovalSQL("e.id", true)
	clauses := []string{"1=1"}
	if q.Folder != "trash" {
		clauses = append(clauses, "NOT "+deleted)
	}
	args := make([]interface{}, 0)
	if q.AccountID > 0 {
		clauses = append(clauses, "e.account_id=?")
		args = append(args, q.AccountID)
	}
	if q.Folder == "" {
		q.Folder = "inbox"
	}
	if q.Folder == "all" {
		clauses = append(clauses, "f.role<>'trash'")
	}
	if q.Folder == "inbox" {
		clauses = append(clauses, "NOT "+pendingRemovalSQL("e.id", true))
	}
	if q.Folder == "trash" || q.Folder == "archive" {
		pending := deleted
		if q.Folder == "archive" {
			pending = archived
		}
		clauses = append(clauses, "(f.role=? OR f.name=? OR "+pending+")")
		args = append(args, q.Folder, q.Folder)
	} else if q.Folder != "all" {
		clauses = append(clauses, "(f.role=? OR f.name=?)")
		args = append(args, q.Folder, q.Folder)
	}
	if q.Unread {
		clauses = append(clauses, "e.is_read=0")
	}
	text := strings.TrimSpace(q.Q)
	if text != "" {
		if utf8.RuneCountInString(text) >= 3 {
			clauses = append(clauses, "e.id IN (SELECT rowid FROM emails_fts WHERE emails_fts MATCH ?)")
			args = append(args, `"`+strings.ReplaceAll(text, `"`, `""`)+`"`)
		} else {
			// Trigram cannot index fewer than three code points; retain substring semantics.
			clauses = append(clauses, `(e.subject LIKE ? ESCAPE '\' OR e.from_name LIKE ? ESCAPE '\' OR e.from_addr LIKE ? ESCAPE '\' OR e.snippet LIKE ? ESCAPE '\')`)
			like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
			args = append(args, like, like, like, like)
		}
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM emails e JOIN folders f ON f.id=e.folder_id WHERE "+strings.Join(clauses, " AND "), args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("storage count filtered emails: %w", err)
	}
	if q.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil {
			return page, ErrInvalidQuery
		}
		var cursor emailCursor
		if err := json.Unmarshal(data, &cursor); err != nil || cursor.ID < 1 {
			return page, ErrInvalidQuery
		}
		clauses = append(clauses, "(e.date < ? OR (e.date = ? AND e.id < ?))")
		args = append(args, cursor.Date, cursor.Date, cursor.ID)
	}
	args = append(args, q.Limit+1)
	columns := strings.TrimSuffix(emailColumns, "f.role") + "CASE WHEN " + deleted + " THEN 'trash' WHEN " + archived + " THEN 'archive' ELSE f.role END,CASE WHEN " + deleted + " THEN 'delete' WHEN " + archived + " THEN 'archive' ELSE '' END"
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+" FROM emails e JOIN folders f ON f.id=e.folder_id WHERE "+strings.Join(clauses, " AND ")+" ORDER BY e.date DESC,e.id DESC LIMIT ?", args...)
	if err != nil {
		return page, fmt.Errorf("storage list emails: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage close email rows: %w", err))
		}
	}()
	for rows.Next() {
		var pending string
		e, err := scanEmail(rows, &pending)
		e.PendingAction = pending
		if err != nil {
			return page, err
		}
		page.Emails = append(page.Emails, e)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("storage email rows: %w", err)
	}
	if len(page.Emails) > q.Limit {
		page.Emails = page.Emails[:q.Limit]
		last := page.Emails[len(page.Emails)-1]
		data, err := json.Marshal(emailCursor{Date: last.Date, ID: last.ID})
		if err != nil {
			return page, fmt.Errorf("storage encode cursor: %w", err)
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}
func (s *Store) Email(ctx context.Context, id int64) (Email, error) {
	return scanEmail(s.DB.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails e JOIN folders f ON f.id=e.folder_id WHERE e.id=?", id))
}
