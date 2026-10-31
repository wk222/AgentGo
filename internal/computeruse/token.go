package computeruse

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Launch binding: the process that starts the sidecar first writes a random
// token to a per-user file; the sidecar re-reads it on every connection and
// the client presents it in the handshake. Together with the pipe's
// owner-only DACL this stops any other local process (or user) from driving
// the desktop. The file lives under the user's cache dir, which Windows ACLs
// to the user by default.

const tokenHexLen = 64 // 32 random bytes

var tokenRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TokenPath is the per-user launch-token file.
func TokenPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating the user cache dir: %w", err)
	}
	return filepath.Join(base, "agentgo", "computer-use", "cua-launch-"+safeName(userKey())+".token"), nil
}

func safeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// ReadToken loads and validates the launch token.
func ReadToken() (string, error) {
	p, err := TokenPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("could not open the launch token (%s): %w", p, err)
	}
	t := string(b)
	for len(t) > 0 && (t[len(t)-1] == '\n' || t[len(t)-1] == '\r' || t[len(t)-1] == ' ') {
		t = t[:len(t)-1]
	}
	switch {
	case t == "":
		return "", fmt.Errorf("launch token is empty")
	case !tokenRe.MatchString(t):
		return "", fmt.Errorf("launch token must be %d lowercase hex characters", tokenHexLen)
	}
	return t, nil
}

// EnsureToken returns the existing token or creates one. Call it before
// starting the sidecar.
func EnsureToken() (string, error) {
	if t, err := ReadToken(); err == nil {
		return t, nil
	}
	p, err := TokenPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, tokenHexLen/2)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("could not retrieve random bytes for the token: %w", err)
	}
	t := hex.EncodeToString(raw)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(t), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return t, nil
}
