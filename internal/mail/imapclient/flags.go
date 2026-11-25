package imapclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/nyakooo/meldmail/internal/storage"
)

type MoveResult struct {
	Acknowledged     bool
	Name, Role       string
	UIDValidity, UID uint32
}

func (c *Client) ApplyFlags(ctx context.Context, a storage.Account, action storage.FlagAction, checkpoint func(MoveResult) error) (*MoveResult, error) {
	e := storage.Email{UID: action.UID}
	if err := c.SelectMessage(ctx, e, action.Folder); err != nil {
		return nil, err
	}
	set := imap.UIDSetNum(imap.UID(action.UID))
	for _, change := range []struct {
		value *bool
		flag  imap.Flag
	}{{action.Change.IsRead, imap.FlagSeen}, {action.Change.IsStarred, imap.FlagFlagged}} {
		if change.value == nil {
			continue
		}
		op := imap.StoreFlagsDel
		if *change.value {
			op = imap.StoreFlagsAdd
		}
		if err := c.IMAP.Store(set, &imap.StoreFlags{Op: op, Flags: []imap.Flag{change.flag}, Silent: true}, nil).Close(); err != nil {
			return nil, fmt.Errorf("imap store flags: %w", err)
		}
	}
	if !action.Change.Archive && !action.Change.Delete && !action.Change.Restore {
		return nil, nil
	}
	role := "archive"
	special := imap.MailboxAttrArchive
	fallback := "Archive"
	if action.Change.Delete {
		role = "trash"
		special = imap.MailboxAttrTrash
		fallback = "Trash"
	}
	if action.Change.Archive && a.Provider == "gmail" {
		special = imap.MailboxAttrAll
		fallback = ""
	}
	target := ""
	if action.Change.Restore {
		role = "inbox"
		fallback = "INBOX"
		special = ""
		target = "INBOX"
	}
	command := c.IMAP.List("", "*", nil)
	for mailbox := command.Next(); mailbox != nil; mailbox = command.Next() {
		for _, attribute := range mailbox.Attrs {
			if attribute == special && target == "" {
				target = mailbox.Mailbox
			}
		}
		if target == "" && strings.EqualFold(mailbox.Mailbox, fallback) && fallback != "" {
			target = mailbox.Mailbox
		}
	}
	if err := command.Close(); err != nil {
		return nil, fmt.Errorf("imap find destination: %w", err)
	}
	if target == "" {
		if fallback == "" {
			return nil, fmt.Errorf("imap: Gmail All Mail mailbox is unavailable")
		}
		target = fallback
		if err := c.IMAP.Create(target, nil).Wait(); err != nil {
			return nil, fmt.Errorf("imap create destination: %w", err)
		}
	}
	if action.Change.Archive && a.Provider == "gmail" {
		return &MoveResult{Name: target, Role: role}, nil
	} // The adapter removes the Inbox label and resolves the All Mail UID.
	if target == action.Folder.Name {
		return nil, fmt.Errorf("imap: message is already in destination")
	}
	if !c.IMAP.Caps().Has(imap.CapMove) && !c.IMAP.Caps().Has(imap.CapUIDPlus) {
		return nil, fmt.Errorf("imap: safe archive/delete requires MOVE or UIDPLUS")
	}
	if !c.IMAP.Caps().Has(imap.CapUIDPlus) && action.MessageID == "" {
		return nil, fmt.Errorf("imap: move UID cannot be resolved without UIDPLUS or Message-ID")
	}
	if err := checkpoint(MoveResult{Name: target, Role: role}); err != nil {
		return nil, err
	}
	moved, err := c.IMAP.Move(set, target).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap move: %w", err)
	}
	acknowledged := MoveResult{Name: target, Role: role, Acknowledged: true, UIDValidity: moved.UIDValidity}
	if destination, ok := moved.DestUIDs.(imap.UIDSet); ok && len(destination) == 1 && destination[0].Start == destination[0].Stop {
		acknowledged.UID = uint32(destination[0].Start)
	}
	if err := checkpoint(acknowledged); err != nil {
		return nil, err
	}
	selected, err := c.IMAP.Select(target, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap destination select: %w", err)
	}
	uid := uint32(0)
	if set, ok := moved.DestUIDs.(imap.UIDSet); ok && len(set) == 1 && set[0].Start == set[0].Stop && moved.UIDValidity == selected.UIDValidity {
		uid = uint32(set[0].Start)
	}

	if uid == 0 {
		if set, ok := moved.DestUIDs.(imap.UIDSet); ok && len(set) == 1 && set[0].Start == set[0].Stop {
			candidate := uint32(set[0].Start)
			matches, err := c.matchesMessageID(candidate, action.MessageID)
			if err != nil {
				return nil, err
			}
			if matches {
				uid = candidate
			}
		}
	}
	if uid == 0 && action.MessageID != "" {
		found, err := c.IMAP.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: strings.Trim(action.MessageID, "<>")}}}, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("imap resolve moved UID: %w", err)
		}
		if set, ok := found.All.(imap.UIDSet); ok && len(set) == 1 && set[0].Start == set[0].Stop {
			uid = uint32(set[0].Start)
		}
	}
	if uid == 0 {
		return nil, fmt.Errorf("imap: destination UID is ambiguous")
	}
	return &MoveResult{Name: target, Role: role, UIDValidity: selected.UIDValidity, UID: uid}, nil
}

// RecoverMove never repeats MOVE after its source UID has disappeared. It uses
// the persisted destination mapping (or an unambiguous Message-ID search).
func (c *Client) RecoverMove(ctx context.Context, action storage.FlagAction) (*MoveResult, error) {
	if err := c.SelectMessage(ctx, storage.Email{UID: action.UID}, action.Folder); err != nil {
		return nil, err
	}
	exists, err := c.hasUID(action.UID)
	if err != nil {
		return nil, err
	}
	if exists {
		if action.MoveAcknowledged {
			return nil, fmt.Errorf("imap: acknowledged move still has a source UID")
		}
		return nil, nil
	}
	selected, err := c.IMAP.Select(action.DestinationName, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap recover destination: %w", err)
	}
	uid := action.DestinationUID
	if uid != 0 && action.DestinationValidity != 0 && selected.UIDValidity != action.DestinationValidity {
		// A provider may put the source UIDVALIDITY in COPYUID. Never trust the
		// destination UID across that mismatch without verifying message identity.
		matches, err := c.matchesMessageID(uid, action.MessageID)
		if err != nil {
			return nil, err
		}
		if !matches {
			uid = 0
		}
	}
	if uid != 0 {
		found, err := c.hasUID(uid)
		if err != nil {
			return nil, err
		}
		if !found {
			uid = 0
		}
	}
	if uid == 0 && action.MessageID != "" {
		data, err := c.IMAP.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: strings.Trim(action.MessageID, "<>")}}}, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("imap recover UID: %w", err)
		}
		if set, ok := data.All.(imap.UIDSet); ok && len(set) == 1 && set[0].Start == set[0].Stop {
			uid = uint32(set[0].Start)
		}
	}
	if uid == 0 {
		return nil, fmt.Errorf("imap: moved message destination cannot yet be resolved")
	}
	return &MoveResult{Name: action.DestinationName, Role: action.DestinationRole, UIDValidity: selected.UIDValidity, UID: uid}, nil
}
func (c *Client) hasUID(uid uint32) (bool, error) {
	// SEARCH handles an absent UID without provoking empty FETCH responses
	// emitted by some providers after a successful MOVE.
	data, err := c.IMAP.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{imap.UIDSetNum(imap.UID(uid))}}, nil).Wait()
	if err != nil {
		return false, fmt.Errorf("imap verify UID: %w", err)
	}
	for _, found := range data.AllUIDs() {
		if uint32(found) == uid {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) matchesMessageID(uid uint32, messageID string) (bool, error) {
	expected := strings.Trim(strings.TrimSpace(messageID), "<>")
	if expected == "" {
		return false, nil
	}
	messages, err := c.IMAP.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return false, fmt.Errorf("imap verify moved identity: %w", err)
	}
	if len(messages) != 1 || uint32(messages[0].UID) != uid || messages[0].Envelope == nil {
		return false, nil
	}
	return strings.Trim(strings.TrimSpace(messages[0].Envelope.MessageID), "<>") == expected, nil
}
