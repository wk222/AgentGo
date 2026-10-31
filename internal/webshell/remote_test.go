package webshell

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const remoteToken = "remote-token-1234567890123456789012345"

func remoteServer(remote bool, token string) http.Handler {
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}}
	return New(Options{Service: fakeService{}, Assets: assets, Token: token, Remote: remote}).Handler()
}

func do(h http.Handler, method, target, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestQRTextAndConnectLinks(t *testing.T) {
	qr, err := QRText("http://192.168.1.5:8788/?access_key=" + remoteToken)
	if err != nil || len(qr) < 100 || !strings.Contains(qr, "\n") {
		t.Fatalf("qr = %q, %v", qr, err)
	}
	for _, l := range ConnectLinks("8788", "a+b") {
		if !strings.HasPrefix(l, "http://") || !strings.HasSuffix(l, ":8788/?access_key=a%2Bb") {
			t.Fatalf("unexpected link %q", l)
		}
	}
}

func TestRemoteOffKeepsLoopbackGuard(t *testing.T) {
	h := remoteServer(false, remoteToken)
	if rec := do(h, "GET", "/api/ipc/ping", "192.168.1.5:8788", nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("LAN host must be refused when remote is off, got %d", rec.Code)
	}
	// 即使带了正确密钥，关闭远程模式时也不接受头里的密钥
	rec := do(h, "GET", "/api/ipc/auth-check", "127.0.0.1:8788", map[string]string{"X-Access-Key": remoteToken}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("header key must be ignored when remote is off, got %d", rec.Code)
	}
}

func TestRemoteNeedsTokenToTakeEffect(t *testing.T) {
	h := remoteServer(true, "")
	if rec := do(h, "GET", "/api/ipc/ping", "192.168.1.5:8788", nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("remote without a token must stay closed, got %d", rec.Code)
	}
}

func TestRemoteAcceptsKeyHeaderAndQuery(t *testing.T) {
	h := remoteServer(true, remoteToken)
	lan := "192.168.1.5:8788"
	if rec := do(h, "GET", "/api/ipc/auth-check", lan, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/ipc/auth-check", lan, map[string]string{"X-Access-Key": "wrong"}, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/ipc/auth-check", lan, map[string]string{"X-Access-Key": remoteToken}, ""); rec.Code != http.StatusOK {
		t.Fatalf("header key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/ipc/auth-check", lan, map[string]string{"Authorization": "Bearer " + remoteToken}, ""); rec.Code != http.StatusOK {
		t.Fatalf("bearer key: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/ipc/auth-check?access_key="+remoteToken, lan, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("query key: %d", rec.Code)
	}
}

func TestRemoteCapacitorOriginNeedsKey(t *testing.T) {
	h := remoteServer(true, remoteToken)
	lan := "192.168.1.5:8788"
	origin := "capacitor://localhost"

	// 预检：浏览器不带密钥，必须能通过并带上 CORS 头
	rec := do(h, "OPTIONS", "/api/ipc/call", lan, map[string]string{"Origin": origin, "Access-Control-Request-Method": "POST"}, "")
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "X-Access-Key") {
		t.Fatalf("preflight must allow the key header: %v", rec.Header())
	}

	// 真正的请求：带密钥才放行
	rec = do(h, "POST", "/api/ipc/call", lan, map[string]string{
		"Origin": origin, "Content-Type": "application/json", "X-Access-Key": remoteToken,
	}, `{"method":"Echo","args":["hi"]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "echo:hi") {
		t.Fatalf("authorised cross-origin call: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf("response must carry CORS header: %v", rec.Header())
	}

	rec = do(h, "POST", "/api/ipc/call", lan, map[string]string{
		"Origin": origin, "Content-Type": "application/json",
	}, `{"method":"Echo","args":["hi"]}`)
	if rec.Code == http.StatusOK {
		t.Fatal("capacitor origin without key must be refused")
	}
}

func TestRemoteRefusesForeignOriginEvenWithKey(t *testing.T) {
	h := remoteServer(true, remoteToken)
	rec := do(h, "POST", "/api/ipc/call", "192.168.1.5:8788", map[string]string{
		"Origin": "https://evil.example", "Content-Type": "application/json", "X-Access-Key": remoteToken,
	}, `{"method":"Echo","args":["hi"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin must be refused: %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("no CORS header for a foreign origin: %v", rec.Header())
	}
}
