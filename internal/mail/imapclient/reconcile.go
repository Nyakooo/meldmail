package imapclient

import (
	"context"
	"fmt"
	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (c *Client) Reconcile(ctx context.Context, s *storage.Store, a storage.Account, notify func(storage.EmailFlags) error) error {
	selected, err := c.IMAP.Select("INBOX", nil).Wait()
	if err != nil {
		return fmt.Errorf("imap reconcile select: %w", err)
	}
	folder, err := s.PrepareFolder(ctx, a.ID, "INBOX", "inbox", selected.UIDValidity)
	if err != nil {
		return err
	}
	after := uint32(0)
	for {
		batch, err := s.FlagBatch(ctx, folder.ID, after)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		var uids imap.UIDSet
		for _, f := range batch {
			uids.AddNum(imap.UID(f.UID))
		}
		received := make(map[uint32]storage.EmailFlags, len(batch))
		cmd := c.IMAP.Fetch(uids, &imap.FetchOptions{UID: true, Flags: true})
		for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
			var flags storage.EmailFlags
			for item := msg.Next(); item != nil; item = msg.Next() {
				switch v := item.(type) {
				case upstream.FetchItemDataUID:
					flags.UID = uint32(v.UID)
				case upstream.FetchItemDataFlags:
					for _, flag := range v.Flags {
						if flag == imap.FlagSeen {
							flags.IsRead = true
						}
						if flag == imap.FlagFlagged {
							flags.IsStarred = true
						}
					}
				}
			}
			received[flags.UID] = flags
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("imap reconcile fetch: %w", err)
		}
		for _, local := range batch {
			remote, ok := received[local.UID]
			remote.ID = local.ID
			remote.Deleted = !ok
			if remote.Deleted || remote.IsRead != local.IsRead || remote.IsStarred != local.IsStarred {
				if err := s.ApplyRemoteFlags(ctx, folder.ID, remote); err != nil {
					return err
				}
				if err := notify(remote); err != nil {
					return err
				}
			}
			after = local.UID
		}
	}
	return s.RefreshUnread(ctx, folder.ID)
}
