package cli

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

const replHelp = `Commands:
  /new              start a new conversation
  /sessions         list saved conversations
  /resume N|ID      switch to a saved conversation (N = number from /sessions)
  /yolo [on|off]    approve every tool call without asking
  /thinking [on|off] show the model's reasoning
  /plugins          list plugins
  /tools            list registered tools
  /help             this text
  /exit             leave (Ctrl+D also works)
End a line with \ to continue it on the next line. Ctrl+C cancels a running turn.`

// repl is the interactive chat loop.
func repl(c *Chat, in *bufio.Reader, env IO, o options) int {
	idle := make(chan struct{}, 1)
	stop := watchInterrupts(c, env.Interrupts, func() {
		select {
		case idle <- struct{}{}:
		default:
		}
	})
	defer stop()

	fmt.Fprintf(env.Err, "agentgo-cli %s  (/help for commands, Ctrl+D to exit)\n", Version)
	if c.Session != "" {
		fmt.Fprintf(env.Err, "session %s\n", c.Session)
	}
	var sessions []SessionInfo // last /sessions listing, for /resume N

	for {
		line, ok := readInput(c, in, env)
		if !ok {
			fmt.Fprintln(env.Err)
			return ExitOK
		}
		line = strings.TrimSpace(line)
		select {
		case <-idle: // Ctrl+C at an empty prompt
			if line == "" {
				return ExitOK
			}
		default:
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			if quit := slash(c, line, env, &sessions); quit {
				return ExitOK
			}
			continue
		}
		out := c.Turn(line)
		switch {
		case out.Interrupted:
			fmt.Fprintln(env.Err, "(cancelled)")
		case out.Error != "":
			fmt.Fprintln(env.Err, "error:", out.Error)
		case len(out.Unresolved) > 0:
			// Only reachable when stdin is not a terminal.
			exitFor(out, c, env)
			return ExitNeedsAnswer
		}
		if !c.R.JSON && out.OK() {
			fmt.Fprintln(env.Out)
		}
	}
}

// readInput reads one logical input (a trailing backslash continues the line).
func readInput(c *Chat, in *bufio.Reader, env IO) (string, bool) {
	var b strings.Builder
	prompt := "> "
	for {
		if env.InTTY {
			fmt.Fprint(env.Err, prompt)
		}
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			if b.Len() > 0 {
				return b.String(), true
			}
			return "", false
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasSuffix(line, "\\") {
			b.WriteString(strings.TrimSuffix(line, "\\"))
			b.WriteByte('\n')
			prompt = ". "
			continue
		}
		b.WriteString(line)
		return b.String(), true
	}
}

// slash runs a REPL command; it reports whether the loop should end.
func slash(c *Chat, line string, env IO, sessions *[]SessionInfo) bool {
	f := strings.Fields(line)
	cmd, arg := f[0], ""
	if len(f) > 1 {
		arg = strings.Join(f[1:], " ")
	}
	switch cmd {
	case "/exit", "/quit", "/q":
		return true
	case "/help", "/?":
		fmt.Fprintln(env.Err, replHelp)
	case "/new":
		c.Session = ""
		fmt.Fprintln(env.Err, "new conversation (created on your first message)")
	case "/sessions":
		list, err := c.B.Sessions()
		if err != nil {
			fmt.Fprintln(env.Err, "error:", err)
			break
		}
		*sessions = list
		for i, s := range list {
			mark := " "
			if s.ID == c.Session {
				mark = "*"
			}
			fmt.Fprintf(env.Err, "%s%2d  %s  %d msgs  %s\n", mark, i+1, s.ID, s.Messages, oneLine(s.Title, 60))
		}
		if len(list) == 0 {
			fmt.Fprintln(env.Err, "no saved conversations")
		}
	case "/resume":
		id := arg
		if n, err := strconv.Atoi(arg); err == nil {
			if n < 1 || n > len(*sessions) {
				fmt.Fprintln(env.Err, "no such entry; run /sessions first")
				break
			}
			id = (*sessions)[n-1].ID
		}
		if id == "" {
			fmt.Fprintln(env.Err, "usage: /resume N|ID")
			break
		}
		c.Session = id
		fmt.Fprintln(env.Err, "session", id)
	case "/yolo":
		c.Yolo = toggle(arg, c.Yolo)
		fmt.Fprintln(env.Err, "yolo", onOff(c.Yolo))
	case "/thinking":
		c.R.Thinking = toggle(arg, c.R.Thinking)
		fmt.Fprintln(env.Err, "thinking", onOff(c.R.Thinking))
	case "/plugins":
		for _, p := range c.B.Plugins() {
			fmt.Fprintf(env.Err, "%-22s %s %s\n", p.Name, p.State, oneLine(p.Error, 80))
		}
	case "/tools":
		fmt.Fprintln(env.Err, strings.Join(c.B.Tools(), "\n"))
	default:
		fmt.Fprintf(env.Err, "unknown command %s (try /help)\n", cmd)
	}
	return false
}

func toggle(arg string, cur bool) bool {
	switch strings.ToLower(arg) {
	case "on", "1", "true", "yes":
		return true
	case "off", "0", "false", "no":
		return false
	}
	return !cur
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
