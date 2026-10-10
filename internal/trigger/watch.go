package trigger

import (
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"time"
)

// A watcher blocks until its condition is met (returning the event detail) or ctx is cancelled (ok=false).
// File checks are local stat/read calls: they cost no tokens and no network.

const filePollInterval = time.Second

func watchTrigger(ctx context.Context, t Trigger) (detail map[string]any, ok bool) {
	switch t.Kind {
	case KindProcessExit:
		return watchProcess(ctx, t.PID)
	case KindFile:
		return watchFile(ctx, t.Path)
	case KindLogMatch:
		return watchLog(ctx, t.Path, t.Pattern)
	default: // webhook: fired from outside
		<-ctx.Done()
		return nil, false
	}
}

func watchFile(ctx context.Context, path string) (map[string]any, bool) {
	tick := time.NewTicker(filePollInterval)
	defer tick.Stop()
	for {
		if st, err := os.Stat(path); err == nil {
			return map[string]any{"path": path, "size": st.Size()}, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-tick.C:
		}
	}
}

// watchLog only looks at lines written AFTER the trigger was armed.
func watchLog(ctx context.Context, path, pattern string) (map[string]any, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, false
	}
	var offset int64 = -1 // -1: file did not exist yet, start from 0 once it appears
	if st, err := os.Stat(path); err == nil {
		offset = st.Size()
	}
	var pending []byte
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-tick.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		if offset < 0 || st.Size() < offset { // appeared late, or truncated/rotated
			offset, pending = 0, nil
		}
		if st.Size() > offset {
			if _, err := f.Seek(offset, io.SeekStart); err == nil {
				buf, _ := io.ReadAll(io.LimitReader(f, 4<<20))
				offset += int64(len(buf))
				pending = append(pending, buf...)
			}
		}
		f.Close()
		for {
			i := bytes.IndexByte(pending, '\n')
			if i < 0 {
				break
			}
			line := string(bytes.TrimRight(pending[:i], "\r"))
			pending = pending[i+1:]
			if re.MatchString(line) {
				return map[string]any{"path": path, "line": line}, true
			}
		}
	}
}
