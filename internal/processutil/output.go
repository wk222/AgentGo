package processutil

import "fmt"

// MaxOutputBytes bounds each captured stream while the process continues to drain.
const MaxOutputBytes = 1 << 20

// OutputBuffer retains at most 1 MiB. It always accepts the full write so a
// verbose child cannot block on a full stdout/stderr pipe or exhaust memory.
type OutputBuffer struct {
	data      []byte
	truncated bool
}

func (b *OutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, MaxOutputBytes-len(b.data))
	b.data = append(b.data, p[:keep]...)
	if keep < n {
		b.truncated = true
	}
	return n, nil
}
func (b *OutputBuffer) String() string {
	if b.truncated {
		return string(b.data) + fmt.Sprintf("\n[AgentGo: output truncated at %d bytes]", MaxOutputBytes)
	}
	return string(b.data)
}
