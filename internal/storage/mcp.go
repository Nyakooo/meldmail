package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type MCPToken struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Hash            string `json:"-"`
	Scope           string `json:"scope"`
	RequireApproval bool   `json:"require_approval"`
	Enabled         bool   `json:"enabled"`
	LastUsedAt      int64  `json:"last_used_at"`
	CreatedAt       int64  `json:"created_at"`
}

const tokenColumns = `id,name,token_hash,scope,require_approval,enabled,last_used_at,created_at`

func scanToken(row scanner) (MCPToken, error) {
	var token MCPToken
	if err := row.Scan(&token.ID, &token.Name, &token.Hash, &token.Scope, &token.RequireApproval, &token.Enabled, &token.LastUsedAt, &token.CreatedAt); err != nil {
		return token, fmt.Errorf("storage MCP token: %w", err)
	}
	return token, nil
}
func (s *Store) CreateMCPToken(ctx context.Context, name, hash, scope string, approval bool) (MCPToken, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO mcp_tokens(name,token_hash,scope,require_approval,created_at) SELECT ?,?,?,?,? WHERE (SELECT COUNT(*) FROM mcp_tokens WHERE enabled=1)<128`, name, hash, scope, approval, time.Now().Unix())
	if err != nil {
		return MCPToken{}, fmt.Errorf("storage create MCP token: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return MCPToken{}, fmt.Errorf("storage token affected: %w", err)
	}
	if n == 0 {
		return MCPToken{}, fmt.Errorf("storage: active token limit reached")
	}
	id, err := result.LastInsertId()
	if err != nil {
		return MCPToken{}, fmt.Errorf("storage MCP token ID: %w", err)
	}
	return s.MCPToken(ctx, id)
}
func (s *Store) MCPToken(ctx context.Context, id int64) (MCPToken, error) {
	return scanToken(s.DB.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM mcp_tokens WHERE id=?", id))
}
func (s *Store) FindMCPToken(ctx context.Context, hash string) (MCPToken, error) {
	return scanToken(s.DB.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM mcp_tokens WHERE token_hash=? AND enabled=1", hash))
}
func (s *Store) MCPTokens(ctx context.Context) (tokens []MCPToken, resultErr error) {
	tokens = make([]MCPToken, 0)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+tokenColumns+" FROM mcp_tokens ORDER BY enabled DESC,id DESC LIMIT 256")
	if err != nil {
		return nil, fmt.Errorf("storage list MCP tokens: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage close token rows: %w", err))
		}
	}()
	for rows.Next() {
		token, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage MCP token rows: %w", err)
	}
	return tokens, nil
}
func (s *Store) TouchMCPToken(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, "UPDATE mcp_tokens SET last_used_at=? WHERE id=? AND enabled=1", time.Now().Unix(), id); err != nil {
		return fmt.Errorf("storage touch MCP token: %w", err)
	}
	return nil
}
func (s *Store) RevokeMCPToken(ctx context.Context, id int64) (resultErr error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage revoke token transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	result, err := tx.ExecContext(ctx, "UPDATE mcp_tokens SET enabled=0 WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("storage revoke token: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pending_actions SET status='rejected',error_msg='Token 已撤销',updated_at=? WHERE token_id=? AND status='pending'", time.Now().Unix(), id); err != nil {
		return fmt.Errorf("storage revoke pending actions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage revoke token commit: %w", err)
	}
	return nil
}

type PendingAction struct {
	ID           string `json:"id"`
	TokenID      int64  `json:"token_id"`
	TokenName    string `json:"token_name"`
	AccountID    int64  `json:"account_id"`
	ActionType   string `json:"action_type"`
	PayloadJSON  string `json:"-"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_msg"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

const actionColumns = `p.id,p.token_id,t.name,p.account_id,p.action_type,p.payload_json,p.status,p.error_msg,p.created_at,p.updated_at`

func scanAction(row scanner) (PendingAction, error) {
	var p PendingAction
	if err := row.Scan(&p.ID, &p.TokenID, &p.TokenName, &p.AccountID, &p.ActionType, &p.PayloadJSON, &p.Status, &p.ErrorMessage, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, fmt.Errorf("storage MCP action: %w", err)
	}
	return p, nil
}
func (s *Store) CreatePendingAction(ctx context.Context, p PendingAction) error {
	now := time.Now().Unix()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO pending_actions(id,token_id,account_id,action_type,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, p.ID, p.TokenID, p.AccountID, p.ActionType, p.PayloadJSON, now, now); err != nil {
		return fmt.Errorf("storage create MCP action: %w", err)
	}
	return nil
}
func (s *Store) PendingAction(ctx context.Context, id string) (PendingAction, error) {
	return scanAction(s.DB.QueryRowContext(ctx, "SELECT "+actionColumns+" FROM pending_actions p JOIN mcp_tokens t ON t.id=p.token_id WHERE p.id=?", id))
}
func (s *Store) PendingActions(ctx context.Context) (actions []PendingAction, resultErr error) {
	actions = make([]PendingAction, 0)
	rows, err := s.DB.QueryContext(ctx, "SELECT "+actionColumns+" FROM pending_actions p JOIN mcp_tokens t ON t.id=p.token_id ORDER BY CASE WHEN p.status IN ('pending','approved') THEN 0 ELSE 1 END,p.created_at DESC,p.id LIMIT 100")
	if err != nil {
		return nil, fmt.Errorf("storage list MCP actions: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	for rows.Next() {
		action, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage MCP action rows: %w", err)
	}
	return actions, nil
}
func (s *Store) ClaimMCPAction(ctx context.Context, id string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE pending_actions SET status='approved',updated_at=? WHERE id=? AND status='pending' AND EXISTS(SELECT 1 FROM mcp_tokens t WHERE t.id=pending_actions.token_id AND t.enabled=1 AND t.scope='readwrite')`, time.Now().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("storage claim MCP action: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage action affected: %w", err)
	}
	return n == 1, nil
}
func (s *Store) FinishMCPAction(ctx context.Context, id, from, to, diagnostic string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, "UPDATE pending_actions SET status=?,error_msg=?,updated_at=? WHERE id=? AND status=?", to, diagnostic, time.Now().Unix(), id, from)
	if err != nil {
		return false, fmt.Errorf("storage finish MCP action: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage action affected: %w", err)
	}
	return n == 1, nil
}
func (s *Store) RecoverMCPActions(ctx context.Context) (resultErr error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage recover MCP transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(token_id,tool_name,args_summary,status,created_at) SELECT token_id,'approval_recovery',id,'error',? FROM pending_actions WHERE status IN ('pending','approved')`, time.Now().Unix()); err != nil {
		return fmt.Errorf("storage recovery audit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pending_actions SET error_msg=CASE WHEN status='approved' THEN '执行期间服务重启；发送结果待确认，请先检查发件箱' ELSE '服务重启，正文未保留；请重新发起请求' END,status='failed',updated_at=? WHERE status IN ('pending','approved')`, time.Now().Unix()); err != nil {
		return fmt.Errorf("storage recover MCP actions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage recovery commit: %w", err)
	}
	return nil
}

type AuditLog struct {
	ID          int64  `json:"id"`
	TokenID     *int64 `json:"token_id"`
	ToolName    string `json:"tool_name"`
	ArgsSummary string `json:"args_summary"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Store) Audit(ctx context.Context, tokenID *int64, tool, summary, status string) error {
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO audit_logs(token_id,tool_name,args_summary,status,created_at) VALUES(?,?,?,?,?)", tokenID, tool, summary, status, time.Now().Unix()); err != nil {
		return fmt.Errorf("storage MCP audit: %w", err)
	}
	return nil
}
func (s *Store) AuditLogs(ctx context.Context) (logs []AuditLog, resultErr error) {
	logs = make([]AuditLog, 0)
	rows, err := s.DB.QueryContext(ctx, "SELECT id,token_id,tool_name,args_summary,status,created_at FROM audit_logs ORDER BY id DESC LIMIT 100")
	if err != nil {
		return nil, fmt.Errorf("storage audit logs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	for rows.Next() {
		var log AuditLog
		if err := rows.Scan(&log.ID, &log.TokenID, &log.ToolName, &log.ArgsSummary, &log.Status, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("storage audit row: %w", err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage audit rows: %w", err)
	}
	return logs, nil
}
