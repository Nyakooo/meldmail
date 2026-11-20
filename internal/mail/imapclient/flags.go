package imapclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/nyakooo/meldmail/internal/storage"
)

type MoveResult struct {
	Name, Role       string
	UIDValidity, UID uint32
}

func (c *Client) ApplyFlags(ctx context.Context, a storage.Account, action storage.FlagAction) (*MoveResult, error) {
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
	if !action.Change.Archive && !action.Change.Delete {
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
	moved, err := c.IMAP.Move(set, target).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap move: %w", err)
	}
	selected, err := c.IMAP.Select(target, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap destination select: %w", err)
	}
	uid := uint32(0)
	if set, ok := moved.DestUIDs.(imap.UIDSet); ok && len(set) == 1 && set[0].Start == set[0].Stop && moved.UIDValidity == selected.UIDValidity {
		uid = uint32(set[0].Start)
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
