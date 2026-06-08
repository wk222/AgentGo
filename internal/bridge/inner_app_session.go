package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/apps"
)

const innerAppSessionTTL = 2 * time.Hour

type innerAppSession struct {
	AppName   string
	Nonce     string
	CreatedAt int64
	ExpiresAt int64
}

func (r *Runtime) openInnerAppSession(ctx context.Context, appName string) (map[string]any, error) {
	if r.appStore == nil {
		return nil, fmt.Errorf("app store unavailable")
	}
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return nil, fmt.Errorf("app name required")
	}
	app, err := r.appStore.GetByName(ctx, appName)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sessionID, err := randomInnerAppToken("ias_")
	if err != nil {
		return nil, err
	}
	nonce, err := randomInnerAppToken("ian_")
	if err != nil {
		return nil, err
	}
	sess := innerAppSession{
		AppName:   app.Name,
		Nonce:     nonce,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(innerAppSessionTTL).Unix(),
	}

	r.mu.Lock()
	if r.innerAppSessions == nil {
		r.innerAppSessions = make(map[string]innerAppSession)
	}
	for id, s := range r.innerAppSessions {
		if s.ExpiresAt <= now.Unix() {
			delete(r.innerAppSessions, id)
		}
	}
	r.innerAppSessions[sessionID] = sess
	r.mu.Unlock()

	return map[string]any{
		"success":    true,
		"session_id": sessionID,
		"nonce":      nonce,
		"created_at": sess.CreatedAt,
		"expires_at": sess.ExpiresAt,
		"ttl_sec":    int(innerAppSessionTTL.Seconds()),
		"manifest":   apps.ManifestFor(app),
	}, nil
}

func (r *Runtime) validateInnerAppSession(appName, sessionID, nonce string) error {
	appName = strings.TrimSpace(appName)
	sessionID = strings.TrimSpace(sessionID)
	nonce = strings.TrimSpace(nonce)
	if appName == "" || sessionID == "" || nonce == "" {
		return fmt.Errorf("inner app session required")
	}

	r.mu.RLock()
	sess, ok := r.innerAppSessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("inner app session not found")
	}
	if time.Now().Unix() > sess.ExpiresAt {
		r.mu.Lock()
		delete(r.innerAppSessions, sessionID)
		r.mu.Unlock()
		return fmt.Errorf("inner app session expired")
	}
	if !strings.EqualFold(sess.AppName, appName) {
		return fmt.Errorf("inner app session app mismatch")
	}
	if sess.Nonce != nonce {
		return fmt.Errorf("inner app session nonce mismatch")
	}
	return nil
}

func randomInnerAppToken(prefix string) (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
