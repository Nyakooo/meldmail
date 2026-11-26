package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Recent returns up to 100 complete entries, newest first, from retained daily logs.
func (f *Files) Recent() ([]json.RawMessage, error) {
	const limit = 100
	const maxTail = 128 << 10
	entries := make([]json.RawMessage, 0, limit)
	now := f.now().UTC()
	for daysAgo := 0; daysAgo < 7 && len(entries) < limit; daysAgo++ {
		day := now.AddDate(0, 0, -daysAgo).Format(time.DateOnly)
		file, err := os.Open(filepath.Join(f.dir, "meldmail-"+day+".jsonl"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("logging open recent: %w", err)
		}
		info, err := file.Stat()
		if err != nil {
			return nil, errors.Join(fmt.Errorf("logging stat recent: %w", err), file.Close())
		}
		size := min(info.Size(), maxTail)
		offset := info.Size() - size
		tail := make([]byte, size)
		n, err := file.ReadAt(tail, offset)
		if errors.Is(err, io.EOF) {
			err = nil
		}
		if err := errors.Join(err, file.Close()); err != nil {
			return nil, fmt.Errorf("logging read recent: %w", err)
		}

		tail = tail[:n]
		if offset > 0 {
			first := bytes.IndexByte(tail, '\n')
			if first < 0 {
				continue
			}
			tail = tail[first+1:]
		}
		// A concurrent append may leave a partial line at the end of the snapshot.
		for end := bytes.LastIndexByte(tail, '\n'); end >= 0 && len(entries) < limit; {
			start := bytes.LastIndexByte(tail[:end], '\n')
			line := tail[start+1 : end]
			if len(line) > 0 && line[0] == '{' && json.Valid(line) {
				entries = append(entries, line)
			}
			end = start
		}
	}
	return entries, nil
}
