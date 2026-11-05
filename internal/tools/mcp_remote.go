package tools

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// The ways an MCP server can be reached.
const (
	mcpTransportStdio = "stdio"
	mcpTransportHTTP  = "http" // Streamable HTTP, the current MCP transport
	mcpTransportSSE   = "sse"  // the older HTTP+SSE transport
)

// mcpTarget says where one MCP server lives. Exactly one of Command (a child
// process spoken to over stdio) or URL (a remote server) is set.
type mcpTarget struct {
	Command string
	Args    []string
	Env     []string

	URL     string
	Headers map[string]string
	// Transport is http or sse; empty means try http first and fall back to sse,
	// which is what most hosted servers need.
	Transport string
}

func (t mcpTarget) remote() bool { return strings.TrimSpace(t.URL) != "" }

// normalizeMCPTransport accepts the spellings found in Claude, Cursor and AionUi
// configs ("streamable-http", "streamableHttp", "http", "sse", "stdio").
func normalizeMCPTransport(s string) (string, error) {
	key := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.TrimSpace(s)))
	switch key {
	case "":
		return "", nil
	case "http", "https", "streamablehttp", "streamable":
		return mcpTransportHTTP, nil
	case "sse":
		return mcpTransportSSE, nil
	case "stdio", "local":
		return mcpTransportStdio, nil
	}
	return "", fmt.Errorf("unsupported MCP transport %q (use http, sse or stdio)", s)
}

// target turns a config entry into something loadable, rejecting ambiguous ones.
func (c MCPServerConfig) target() (mcpTarget, error) {
	kind, err := normalizeMCPTransport(c.Type)
	if err != nil {
		return mcpTarget{}, err
	}
	command, rawURL := strings.TrimSpace(c.Command), strings.TrimSpace(c.URL)
	if kind == "" {
		switch {
		case command != "" && rawURL != "":
			return mcpTarget{}, fmt.Errorf("set either command (stdio) or url (remote), not both")
		case rawURL != "":
			// leave kind empty: auto-detect between http and sse
		case command != "":
			kind = mcpTransportStdio
		default:
			return mcpTarget{}, fmt.Errorf("command (stdio) or url (remote) is required")
		}
	}
	if kind == mcpTransportStdio {
		if command == "" {
			return mcpTarget{}, fmt.Errorf("command is required for a stdio server")
		}
		t := mcpTarget{Command: command, Args: c.Args}
		if len(c.Env) > 0 {
			t.Env = append(t.Env, os.Environ()...)
			for k, v := range c.Env {
				t.Env = append(t.Env, k+"="+v)
			}
		}
		return t, nil
	}
	if rawURL == "" {
		return mcpTarget{}, fmt.Errorf("url is required for a %s server", kind)
	}
	return mcpTarget{URL: rawURL, Headers: c.Headers, Transport: kind}, nil
}

var mcpEnvRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandMCPEnv replaces ${NAME} with the environment variable's value. Unset or
// empty variables are collected rather than silently becoming "", so a header can
// never go out as a bare "Bearer ".
func expandMCPEnv(s string, missing map[string]struct{}) string {
	return mcpEnvRefRe.ReplaceAllStringFunc(s, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if v := os.Getenv(name); v != "" {
			return v
		}
		missing[name] = struct{}{}
		return ""
	})
}

var mcpHeaderNameRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// resolveMCPRemote expands ${ENV} references in the URL and headers and checks
// them. Error messages name variables, never values.
func resolveMCPRemote(rawURL string, headers map[string]string) (string, map[string]string, error) {
	missing := map[string]struct{}{}
	expandedURL := expandMCPEnv(strings.TrimSpace(rawURL), missing)
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		k = strings.TrimSpace(k)
		if !mcpHeaderNameRe.MatchString(k) {
			return "", nil, fmt.Errorf("invalid header name %q", k)
		}
		value := expandMCPEnv(v, missing)
		if strings.ContainsAny(value, "\r\n") {
			return "", nil, fmt.Errorf("header %q contains a line break", k)
		}
		out[k] = value
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", nil, fmt.Errorf("environment variable(s) %s referenced by the MCP config are not set", strings.Join(names, ", "))
	}
	u, err := url.Parse(expandedURL)
	if err != nil {
		return "", nil, fmt.Errorf("invalid MCP url: %v", redactURLError(err))
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", nil, fmt.Errorf("MCP url must be an absolute http(s) address")
	}
	return expandedURL, out, nil
}

// redactURLError drops the URL that net/url embeds in its errors; it may carry a token.
func redactURLError(err error) string {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err.Error()
	}
	return err.Error()
}

// redactMCPURL is the form of a remote address that is safe to show: no
// credentials, query string or fragment (tokens often live there).
func redactMCPURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

var mcpURLQueryRe = regexp.MustCompile(`(https?://[^\s"'?]*)\?[^\s"']*`)

// scrubMCPText removes what must not reach a log line or a tool result: query
// strings of URLs (tokens often live there) and the header values in use.
func scrubMCPText(msg string, secrets []string) string {
	msg = mcpURLQueryRe.ReplaceAllString(msg, "$1?…")
	for _, s := range secrets {
		if len(s) >= 4 {
			msg = strings.ReplaceAll(msg, s, "***")
		}
	}
	return msg
}

// scrubbedError carries the cleaned text but still unwraps to the cause, so
// errors.Is(err, context.DeadlineExceeded) keeps working.
type scrubbedError struct {
	msg string
	err error
}

func (e scrubbedError) Error() string { return e.msg }
func (e scrubbedError) Unwrap() error { return e.err }

// mcpTransportLogger is handed to the transports, which log request URLs themselves.
type mcpTransportLogger struct{ secrets []string }

func (l mcpTransportLogger) Infof(format string, v ...any) {
	log.Print("[agentgo-mcp] INFO: " + scrubMCPText(fmt.Sprintf(format, v...), l.secrets))
}

func (l mcpTransportLogger) Errorf(format string, v ...any) {
	log.Print("[agentgo-mcp] ERROR: " + scrubMCPText(fmt.Sprintf(format, v...), l.secrets))
}

func headerSecrets(headers map[string]string) []string {
	out := make([]string, 0, len(headers))
	for _, v := range headers {
		out = append(out, v)
	}
	return out
}

// remoteCloser tears a remote client down once, including the context that keeps
// its streams alive.
type remoteCloser struct {
	once sync.Once
	stop context.CancelFunc
	c    *client.Client
	err  error
}

func (r *remoteCloser) Close() error {
	r.once.Do(func() {
		r.stop()
		r.err = r.c.Close()
	})
	return r.err
}

// startMCPRemote opens one transport. The transport has to outlive the load call, so
// it runs on its own context; ctx only aborts a start that hangs.
func startMCPRemote(ctx context.Context, kind, rawURL string, headers map[string]string) (*client.Client, io.Closer, error) {
	var (
		c   *client.Client
		err error
	)
	logger := mcpTransportLogger{secrets: headerSecrets(headers)}
	switch kind {
	case mcpTransportSSE:
		c, err = client.NewSSEMCPClient(rawURL, transport.WithHeaders(headers), transport.WithSSELogger(logger))
	default:
		c, err = client.NewStreamableHttpClient(rawURL, transport.WithHTTPHeaders(headers), transport.WithHTTPLogger(logger))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%v", redactURLError(err))
	}
	life, stop := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-started:
		}
	}()
	err = c.Start(life)
	close(started)
	if err != nil {
		stop()
		_ = c.Close()
		return nil, nil, err
	}
	return c, &remoteCloser{stop: stop, c: c}, nil
}

func initializeMCP(ctx context.Context, c *client.Client) error {
	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "agentgo", Version: "1.0.0"}
	_, err := c.Initialize(ctx, request)
	return err
}

// connectMCP starts a server and completes the MCP handshake. It returns the
// transport that ended up in use.
func connectMCP(ctx context.Context, t mcpTarget) (*client.Client, io.Closer, string, error) {
	if !t.remote() {
		c, closer, err := startMCPProcess(t.Command, t.Args, t.Env)
		if err != nil {
			return nil, nil, "", fmt.Errorf("create: %w", err)
		}
		if err = initializeMCP(ctx, c); err != nil {
			_ = closer.Close()
			return nil, nil, "", fmt.Errorf("initialize: %w", err)
		}
		return c, closer, mcpTransportStdio, nil
	}
	rawURL, headers, err := resolveMCPRemote(t.URL, t.Headers)
	if err != nil {
		return nil, nil, "", err
	}
	kinds := []string{t.Transport}
	if t.Transport == "" {
		kinds = []string{mcpTransportHTTP, mcpTransportSSE}
	}
	var failures []string
	var first error
	for _, kind := range kinds {
		c, closer, err := startMCPRemote(ctx, kind, rawURL, headers)
		if err == nil {
			if err = initializeMCP(ctx, c); err != nil {
				_ = closer.Close()
			}
		}
		if err == nil {
			return c, closer, kind, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", kind, err))
		if first == nil {
			first = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	msg := scrubMCPText(strings.Join(failures, "; "), headerSecrets(headers))
	return nil, nil, "", withAuthHint(scrubbedError{msg: msg, err: first})
}

var mcpAuthFailureRe = regexp.MustCompile(`(?i)status(?: code)?:? (?:401|403)\b|unauthorized|forbidden`)

// withAuthHint turns a bare 401/403 into something the model can act on.
func withAuthHint(err error) error {
	if mcpAuthFailureRe.MatchString(err.Error()) {
		return fmt.Errorf("%w (the server wants credentials: add an Authorization header, e.g. \"Bearer ${MY_TOKEN}\"; interactive OAuth login is not supported yet)", err)
	}
	return err
}
