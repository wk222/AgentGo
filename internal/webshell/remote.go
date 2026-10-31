package webshell

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// 远程模式（给手机端等非本机客户端用）。默认关闭；开启后：
//   - 不再要求 Host 是回环地址（否则手机根本连不上），但 /api/ipc/* 必须带访问密钥；
//   - 访问密钥可以放在 X-Access-Key / Authorization: Bearer 头里，或 ?access_key= 查询参数
//     （EventSource 没法设请求头）；
//   - 允许 Capacitor WebView 的来源跨域访问，但只有请求带着正确密钥才放行。
//
// 密钥是 128+ 位随机数，攻击者没有它就既拿不到 API，也无法借浏览器发起 CSRF——
// 这也是为什么远程模式下 Cookie 不再被当作跨域请求的凭据。

// mobileOrigins 是 Capacitor WebView 页面的来源。
var mobileOrigins = map[string]bool{
	"capacitor://localhost": true, // iOS
	"https://localhost":     true, // Android
	"http://localhost":      true, // Android（allowMixedContent / 旧版配置）
}

// remote 报告远程模式是否真正生效：必须显式开启并且有访问密钥。
func (s *Server) remote() bool { return s.opts.Remote && s.opts.Token != "" }

// requestKey 取出请求里的访问密钥（头优先，其次查询参数）。
func requestKey(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-Access-Key")); k != "" {
		return k
	}
	if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "Bearer ") {
		return strings.TrimSpace(a[7:])
	}
	return r.URL.Query().Get("access_key")
}

// keyAuthorized 报告请求是否带着正确的访问密钥（仅远程模式有意义）。
func (s *Server) keyAuthorized(r *http.Request) bool {
	if !s.remote() {
		return false
	}
	k := requestKey(r)
	return k != "" && subtle.ConstantTimeCompare([]byte(k), []byte(s.opts.Token)) == 1
}

// crossOriginAllowed 判断带 Origin 的请求能否放行：同源总是可以；
// 远程模式下，来自 Capacitor WebView 且带正确密钥的请求也可以。
func (s *Server) crossOriginAllowed(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err == nil && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return s.remote() && mobileOrigins[origin] && s.keyAuthorized(r)
}

// applyCORS 给远程模式下来自 Capacitor 的请求写 CORS 头，并直接回应预检请求。
// 返回 true 表示请求已处理完毕。
func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	if !s.remote() {
		return false
	}
	origin := r.Header.Get("Origin")
	if !mobileOrigins[origin] {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Add("Vary", "Origin")
	h.Set("Access-Control-Allow-Headers", "Content-Type, X-Access-Key, Authorization")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Max-Age", "600")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// ConnectLinks 返回手机端可以扫码/粘贴的连接链接：每个本机的非回环 IPv4 一条
// （局域网、Tailscale 的 100.x 都会列出来）。
func ConnectLinks(port, token string) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, "http://"+net.JoinHostPort(ipn.IP.String(), port)+"/?access_key="+url.QueryEscape(token))
	}
	return out
}

// QRText 把链接渲染成能直接打印在终端里的二维码（半高字符，两行并一行），
// 手机端 App 扫它就能连上，不用手敲 48 位密钥。
func QRText(link string) (string, error) {
	q, err := qrcode.New(link, qrcode.Low)
	if err != nil {
		return "", err
	}
	return q.ToSmallString(false), nil
}

// serveAuthCheck 是手机端连接时的探针：必须通过鉴权才返回 200，
// 所以密钥错误会得到 401，而不是像 /api/ipc/ping 那样不管密钥都通过。
func (s *Server) serveAuthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": "agentgo-web"})
}
