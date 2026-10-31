package tools

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"

	"agentgo/internal/processutil"
	"agentgo/internal/shellcmd"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

type mcpProcess struct {
	client     *client.Client
	tree       shellcmd.ProcessTree
	once       sync.Once
	err        error
	pipes      []io.Closer
	stderrDone chan struct{}
}

func (p *mcpProcess) Close() error {
	p.once.Do(func() {
		// NewIO closes protocol pipes without waiting on an uncooperative
		// child. The host's existing ProcessTree owns termination and Wait.
		p.err = p.client.Close()
		for _, pipe := range p.pipes {
			_ = pipe.Close()
		}
		p.tree.Kill()
		_ = p.tree.Wait()
		<-p.stderrDone
	})
	return p.err
}

func startMCPProcess(command string, args, env []string) (*client.Client, io.Closer, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), env...)
	processutil.HideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, err
	}
	tree, err := shellcmd.StartTree(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, nil, err
	}
	c := client.NewClient(transport.NewIO(stdout, stdin, stderr))
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, stderr); close(done) }()
	p := &mcpProcess{client: c, tree: tree, pipes: []io.Closer{stdin, stdout, stderr}, stderrDone: done}
	if err = c.Start(context.Background()); err != nil {
		_ = p.Close()
		return nil, nil, err
	}
	return c, p, nil
}
