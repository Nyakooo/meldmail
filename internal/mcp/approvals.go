package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
)

// approvalMetadata deliberately has no body fields. The schema's payload_json
// stores only metadata and a digest; complete mail content is never persisted.
type approvalMetadata struct {
	SenderEmail string   `json:"sender_email"`
	AccountID   int64    `json:"account_id"`
	EmailID     int64    `json:"email_id,omitempty"`
	Action      string   `json:"action"`
	To          []string `json:"to"`
	CC          []string `json:"cc,omitempty"`
	Subject     string   `json:"subject"`
	BodySHA256  string   `json:"body_sha256"`
}
type ActionView struct {
	SenderEmail string `json:"sender_email"`
	storage.PendingAction
	Payload       mail.SendInput `json:"payload"`
	BodyAvailable bool           `json:"body_available"`
	ExpiresAt     int64          `json:"expires_at"`
}

func cloneInput(input mail.SendInput) mail.SendInput {
	input.To = append([]string(nil), input.To...)
	input.CC = append([]string(nil), input.CC...)
	return input
}
func (s *Server) expireLocked(ctx context.Context) error {
	for id, entry := range s.pending {
		if entry.executing || time.Since(entry.created) < approvalTTL {
			continue
		}
		changed, err := s.store.FinishMCPAction(ctx, id, "pending", "failed", "审批已过期，正文已丢弃")
		if err != nil {
			return fmt.Errorf("mcp expire action: %w", err)
		}
		if changed {
			action, err := s.store.PendingAction(ctx, id)
			if err != nil {
				return fmt.Errorf("mcp load pending action: %w", err)
			}
			if err := s.store.Audit(ctx, &action.TokenID, "approval_expired", id, "error"); err != nil {
				return err
			}
			if err := s.publish(id, "failed"); err != nil {
				return err
			}
		}
		s.pendingBytes -= entry.size
		delete(s.pending, id)
	}
	return nil
}
func (s *Server) enqueue(ctx context.Context, token storage.MCPToken, input mail.SendInput) (string, error) {
	size := len(input.Text) + len(input.HTML) + len(input.Subject) + 128
	for _, values := range [][]string{input.To, input.CC} {
		for _, value := range values {
			size += len(value)
		}
	}
	account, err := s.store.Account(ctx, input.AccountID)
	if err != nil {
		return "", fmt.Errorf("mcp load approval sender: %w", err)
	}
	digest := sha256.Sum256([]byte(input.Text + "\x00" + input.HTML))
	metadata := approvalMetadata{SenderEmail: account.Email, AccountID: input.AccountID, EmailID: input.EmailID, Action: input.Action, To: input.To, CC: input.CC, Subject: input.Subject, BodySHA256: hex.EncodeToString(digest[:])}
	persistent, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("mcp approval metadata: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expireLocked(ctx); err != nil {
		return "", err
	}
	if len(s.pending) >= 32 || s.pendingBytes+size > maxApprovalBytes {
		return "", ErrCapacity
	}
	id, err := randomValue(18)
	if err != nil {
		return "", err
	}
	action := storage.PendingAction{ID: id, TokenID: token.ID, AccountID: input.AccountID, ActionType: input.Action, PayloadJSON: string(persistent)}
	if err := s.store.CreatePendingAction(ctx, action); err != nil {
		return "", fmt.Errorf("mcp enqueue approval: %w", err)
	}
	s.pending[id] = &heldAction{input: cloneInput(input), size: size, created: time.Now()}
	s.pendingBytes += size
	if err := s.publish(id, "pending"); err != nil {
		return "", err
	}
	return id, nil
}
func (s *Server) publish(id, status string) error {
	return s.mail.Events.Publish("mcp_approval_required", map[string]string{"action_id": id, "status": status})
}
func (s *Server) Approvals(ctx context.Context) ([]ActionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expireLocked(ctx); err != nil {
		return nil, err
	}
	actions, err := s.store.PendingActions(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp list approvals: %w", err)
	}
	views := make([]ActionView, 0, len(actions))
	for _, action := range actions {
		var metadata approvalMetadata
		if err := json.Unmarshal([]byte(action.PayloadJSON), &metadata); err != nil {
			return nil, fmt.Errorf("mcp approval metadata decode: %w", err)
		}
		view := ActionView{SenderEmail: metadata.SenderEmail, PendingAction: action, Payload: mail.SendInput{AccountID: metadata.AccountID, EmailID: metadata.EmailID, Action: metadata.Action, To: metadata.To, CC: metadata.CC, Subject: metadata.Subject}, ExpiresAt: action.CreatedAt + int64(approvalTTL/time.Second)}
		if _, ok := s.pending[action.ID]; ok {
			view.BodyAvailable = true
		}
		views = append(views, view)
	}
	return views, nil
}
func (s *Server) Approve(ctx context.Context, id string) (resultErr error) {
	s.mu.Lock()
	if err := s.expireLocked(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	entry, ok := s.pending[id]
	if !ok || entry.executing {
		s.mu.Unlock()
		return ErrConflict
	}
	action, err := s.store.PendingAction(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("mcp load approval: %w", err)
	}
	token, err := s.store.MCPToken(ctx, action.TokenID)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("mcp load approval token: %w", err)
	}
	if !token.Enabled || token.Scope != "readwrite" {
		s.mu.Unlock()
		return ErrForbidden
	}
	claimed, err := s.store.ClaimMCPAction(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("mcp claim approval: %w", err)
	}
	if !claimed {
		s.mu.Unlock()
		return ErrConflict
	}
	entry.executing = true
	input := cloneInput(entry.input)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pendingBytes -= entry.size; delete(s.pending, id); s.mu.Unlock() }()
	// Approved is a one-way claim. Browser retries cannot execute SMTP twice.
	var metadata approvalMetadata
	var sendErr error
	if err := json.Unmarshal([]byte(action.PayloadJSON), &metadata); err != nil {
		sendErr = err
	} else if account, err := s.store.Account(ctx, input.AccountID); err != nil {
		sendErr = err
	} else if account.Email != metadata.SenderEmail {
		sendErr = ErrInvalid
	} else {
		sendErr = s.mail.Send(ctx, input)
	}
	status := "executed"
	auditStatus := "success"
	diagnostic := ""
	if sendErr != nil {
		status = "failed"
		auditStatus = "error"
		diagnostic = "SMTP 未完成确认；请检查账号连接和发件箱，确认后再重新提交"
	}
	// Persist the outcome even when the approving browser disconnected. This is
	// metadata-only and bounded; the SMTP operation itself follows request context.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	changed, err := s.store.FinishMCPAction(finish, id, "approved", status, diagnostic)
	if err != nil {
		return fmt.Errorf("mcp record approval outcome: %w", err)
	}
	if !changed {
		return ErrConflict
	}
	auditErr := s.store.Audit(finish, &action.TokenID, input.Action+"_email", fmt.Sprintf(`{"action_id":%q}`, id), auditStatus)
	if auditErr != nil {
		if sendErr != nil {
			return errors.Join(fmt.Errorf("mcp approved delivery: %w", sendErr), fmt.Errorf("mcp audit outcome: %w", auditErr))
		}
		return errors.Join(ErrAuditOutcome, fmt.Errorf("mcp audit outcome: %w", auditErr))
	}
	if err := s.publish(id, status); err != nil {
		return err
	}
	if sendErr != nil {
		return fmt.Errorf("mcp approved delivery: %w", sendErr)
	}
	return nil
}
func (s *Server) Reject(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejectLocked(ctx, id)
}

func (s *Server) RejectBatch(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return 0, ErrInvalid
	}
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return 0, ErrInvalid
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.expireLocked(ctx); err != nil {
		return 0, err
	}
	rejected := 0
	for _, id := range unique {
		err := s.rejectLocked(ctx, id)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return rejected, err
		}
		rejected++
	}
	if rejected == 0 {
		return 0, ErrConflict
	}
	return rejected, nil
}

func (s *Server) rejectLocked(ctx context.Context, id string) error {
	entry, ok := s.pending[id]
	if !ok || entry.executing {
		return ErrConflict
	}
	action, err := s.store.PendingAction(ctx, id)
	if err != nil {
		return err
	}
	changed, err := s.store.FinishMCPAction(ctx, id, "pending", "rejected", "")
	if err != nil {
		return err
	}
	if !changed {
		return ErrConflict
	}
	s.pendingBytes -= entry.size
	delete(s.pending, id)
	if err := s.store.Audit(ctx, &action.TokenID, "approval_rejected", id, "denied"); err != nil {
		return fmt.Errorf("mcp audit rejection: %w", err)
	}
	return s.publish(id, "rejected")
}
func (s *Server) RevokeToken(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.RevokeMCPToken(ctx, id); err != nil {
		return fmt.Errorf("mcp revoke token: %w", err)
	}
	for actionID, entry := range s.pending {
		if entry.executing {
			continue
		}
		action, err := s.store.PendingAction(ctx, actionID)
		if err != nil {
			return err
		}
		if action.TokenID == id {
			s.pendingBytes -= entry.size
			delete(s.pending, actionID)
			if err := s.publish(actionID, "rejected"); err != nil {
				return err
			}
		}
	}
	if err := s.store.Audit(ctx, &id, "token_revoked", "{}", "success"); err != nil {
		return fmt.Errorf("mcp audit revocation: %w", err)
	}
	return nil
}

func (s *Server) DeleteRevokedToken(ctx context.Context, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.DeleteRevokedMCPToken(ctx, id)
}

// Review serializes the full body while holding the same bounded memory gate
// as MCP responses. Collection endpoints return only body-free metadata.
func (s *Server) Review(ctx context.Context, id string, consume func(ActionView) error) error {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.gate }()
	views, err := s.Approvals(ctx)
	if err != nil {
		return err
	}
	for _, view := range views {
		if view.ID != id {
			continue
		}
		s.mu.Lock()
		entry, ok := s.pending[id]
		if ok {
			view.Payload = cloneInput(entry.input)
		}
		s.mu.Unlock()
		return consume(view)
	}
	return ErrConflict
}
