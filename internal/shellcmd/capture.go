package shellcmd

import (
	"fmt"
	"sync"
)

// Capture collects a command's output without holding all of it. It keeps the
// start (what the command announced) and the end (where its errors usually
// are) and says how much lay between. It always accepts everything written, so
// a chatty child never blocks on a full pipe, and the agent never holds
// gigabytes of a runaway loop's output. It stores at most about 1.5 times max
// bytes. Safe for concurrent use.
type Capture struct {
	mu    sync.Mutex
	half  int
	head  []byte
	tail  []byte
	total int64
}

// NewCapture keeps up to max bytes (at least 2) of what is written to it.
func NewCapture(max int) *Capture {
	if max < 2 {
		max = 2
	}
	return &Capture{half: max / 2}
}

// Write implements io.Writer; it never fails and never writes short.
func (c *Capture) Write(p []byte) (int, error) {
	n := len(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(n)
	if room := c.half - len(c.head); room > 0 {
		take := min(room, len(p))
		c.head = append(c.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		c.tail = append(c.tail, p...)
		if len(c.tail) > 2*c.half { // trim now and then, not on every write
			c.tail = append(c.tail[:0], c.tail[len(c.tail)-c.half:]...)
		}
	}
	return n, nil
}

// Dropped is how many bytes of output are not in String.
func (c *Capture) Dropped() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.droppedLocked()
}

func (c *Capture) droppedLocked() int64 {
	return c.total - int64(len(c.head)+len(c.keptTail()))
}

func (c *Capture) keptTail() []byte {
	if len(c.tail) > c.half {
		return c.tail[len(c.tail)-c.half:]
	}
	return c.tail
}

// String is the output; when part of it was dropped, a marker says how much.
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	tail := c.keptTail()
	if dropped := c.droppedLocked(); dropped > 0 {
		return string(c.head) + fmt.Sprintf("\n[... %d bytes of output dropped ...]\n", dropped) + string(tail)
	}
	return string(c.head) + string(tail)
}
