package imapclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/emersion/go-imap/v2"
)

var ErrTargetedExpungeUnsupported = errors.New("mail server does not support targeted expunge")

func (c *Client) TrashMailboxes(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var options *imap.ListOptions
	if c.IMAP.Caps().Has(imap.CapSpecialUse) {
		options = &imap.ListOptions{ReturnSpecialUse: true}
	}
	var names []string
	var fallback string
	cmd := c.IMAP.List("", "*", options)
	for entry := cmd.Next(); entry != nil; entry = cmd.Next() {
		special, selectable := false, true
		for _, attr := range entry.Attrs {
			special = special || attr == imap.MailboxAttrTrash
			selectable = selectable && attr != imap.MailboxAttrNoSelect && attr != imap.MailboxAttrNonExistent
		}
		if !selectable || strings.EqualFold(entry.Mailbox, "INBOX") {
			continue
		}
		if special {
			names = append(names, entry.Mailbox)
		} else if strings.EqualFold(entry.Mailbox, "Trash") {
			fallback = entry.Mailbox
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap list trash folders: %w", err)
	}
	if len(names) == 0 && fallback != "" {
		names = append(names, fallback)
	}
	return names, nil
}

func (c *Client) PurgeTrash(ctx context.Context, name string, validity, uid uint32) (uint32, uint32, error) {
	return c.purgeTrash(ctx, name, validity, uid, nil)
}

func (c *Client) PurgeTrashBatch(ctx context.Context, name string, validity uint32, uids []uint32) (uint32, error) {
	if len(uids) == 0 {
		return validity, nil
	}
	gotValidity, _, err := c.purgeTrash(ctx, name, validity, 0, uids)
	return gotValidity, err
}

func (c *Client) purgeTrash(ctx context.Context, name string, validity, uid uint32, uids []uint32) (uint32, uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	selected, err := c.IMAP.Select(name, nil).Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("imap select trash: %w", err)
	}
	if selected.UIDValidity == 0 || (uid != 0 || len(uids) > 0) && selected.UIDValidity != validity {
		return 0, 0, ErrMessageGone
	}
	upper := uid
	var set imap.UIDSet
	switch {
	case len(uids) > 0:
		for _, item := range uids {
			set.AddNum(imap.UID(item))
		}
	case uid != 0:
		set.AddNum(imap.UID(uid))
	default:
		if selected.UIDNext == 0 {
			return 0, 0, ErrTargetedExpungeUnsupported
		}
		upper = uint32(selected.UIDNext - 1)
		if upper != 0 && selected.NumMessages != 0 {
			set.AddRange(1, imap.UID(upper))
		}
	}
	if len(set) == 0 {
		return selected.UIDValidity, upper, nil
	}
	if !c.IMAP.Caps().Has(imap.CapUIDPlus) && !c.IMAP.Caps().Has(imap.CapIMAP4rev2) {
		return 0, 0, ErrTargetedExpungeUnsupported
	}
	if err := c.IMAP.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}, nil).Close(); err != nil {
		return 0, 0, fmt.Errorf("imap mark trash deleted: %w", err)
	}
	if err := c.IMAP.UIDExpunge(set).Close(); err != nil {
		return 0, 0, fmt.Errorf("imap expunge trash UIDs: %w", err)
	}
	return selected.UIDValidity, upper, nil
}
