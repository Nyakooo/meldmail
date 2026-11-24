package storage

import (
	"context"
	"errors"
	"fmt"
)

type AccountCount struct {
	ID     int64  `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"display_name"`
	Total  int64  `json:"total"`
	Unread int64  `json:"unread"`
}
type MailSummary struct {
	Total    int64          `json:"total"`
	Unread   int64          `json:"unread"`
	Accounts []AccountCount `json:"accounts"`
}

func (s *Store) MailSummary(ctx context.Context) (summary MailSummary, resultErr error) {
	summary.Accounts = make([]AccountCount, 0)
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,a.email,a.display_name,COUNT(e.id),COALESCE(SUM(CASE WHEN e.id IS NOT NULL AND e.is_read=0 THEN 1 ELSE 0 END),0) FROM accounts a LEFT JOIN emails e ON e.account_id=a.id GROUP BY a.id ORDER BY a.id`)
	if err != nil {
		return summary, fmt.Errorf("storage mail summary: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage close summary: %w", err))
		}
	}()
	for rows.Next() {
		var count AccountCount
		if err := rows.Scan(&count.ID, &count.Email, &count.Name, &count.Total, &count.Unread); err != nil {
			return summary, fmt.Errorf("storage scan summary: %w", err)
		}
		summary.Accounts = append(summary.Accounts, count)
		summary.Total += count.Total
		summary.Unread += count.Unread
	}
	if err := rows.Err(); err != nil {
		return summary, fmt.Errorf("storage summary rows: %w", err)
	}
	return summary, nil
}
func (s *Store) DatabasePath(ctx context.Context) (path string, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("storage database files: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage close database files: %w", err))
		}
	}()
	for rows.Next() {
		var number int
		var name, file string
		if err := rows.Scan(&number, &name, &file); err != nil {
			return "", fmt.Errorf("storage scan database files: %w", err)
		}
		if name == "main" {
			path = file
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("storage database file rows: %w", err)
	}
	return path, nil
}
