package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"agentgo/internal/config"
	"golang.org/x/net/html/charset"
)

const webBodyLimit = 2 * 1024 * 1024

type webReadError struct {
	code      string
	message   string
	retryable bool
}

func (e *webReadError) Error() string { return e.message }
func webError(code, message string, retryable bool) error {
	return &webReadError{code, message, retryable}
}

func normalizeWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(raw) > 8192 {
		return nil, webError("invalid_url", "an absolute HTTP(S) URL without credentials is required", false)
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

type webNetworkPolicy struct{ privateHosts map[string]bool }

func newWebNetworkPolicy() webNetworkPolicy {
	p := webNetworkPolicy{privateHosts: map[string]bool{}}
	for _, host := range strings.Split(config.WebFetchPrivateHosts.Get(), ",") {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if host != "" {
			p.privateHosts[host] = true
		}
	}
	return p
}

// Validate every resolved address, then dial those exact IPs. Checking DNS and
// asking the default transport to resolve again permits DNS rebinding.
func (p webNetworkPolicy) addresses(ctx context.Context, host string) ([]netip.Addr, error) {
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
	if err != nil {
		return nil, webError("dns_failed", "page hostname could not be resolved", true)
	}
	if len(ips) == 0 {
		return nil, webError("dns_failed", "page hostname has no addresses", true)
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if !p.privateHosts[name] && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || specialWebAddress(ip)) {
			return nil, webError("network_policy", "private or special-use destination requires an exact operator-configured host allowance", false)
		}
	}
	return ips, nil
}

var specialWebNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"),
	// Forms Go's IsPrivate/IsLoopback/IsGlobalUnicast do not look inside:
	// Teredo (embeds an IPv4 endpoint), deprecated site-local, and the
	// deprecated IPv4-compatible ::a.b.c.d.
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("::/96"),
}

func specialWebAddress(ip netip.Addr) bool {
	for _, prefix := range specialWebNetworks {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (p webNetworkPolicy) client() *http.Client {
	transport := &http.Transport{
		// No ambient proxy: its own DNS/route could bypass destination checks.
		Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := p.addresses(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ip := range ips {
				conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return webError("redirect_limit", "page exceeded five redirects", false)
		}
		_, err := normalizeWebURL(req.URL.String())
		return err // DialContext independently checks the new destination.
	}}
}

type webPage struct{ URL, Title, Text string }

func fetchWebPage(ctx context.Context, client *http.Client, raw string) (webPage, error) {
	u, err := normalizeWebURL(raw)
	if err != nil {
		return webPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return webPage{}, err
	}
	req.Header.Set("User-Agent", "AgentGo/1.0 (page reader)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,text/markdown;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return webPage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return webPage{}, webError(fmt.Sprintf("http_%d", resp.StatusCode), fmt.Sprintf("page returned HTTP %d", resp.StatusCode), resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500)
	}
	if resp.ContentLength > webBodyLimit {
		return webPage{}, webError("body_too_large", "page exceeds the 2 MiB body limit", false)
	}
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, webBodyLimit+1))
	if err != nil {
		return webPage{}, err
	}
	if len(rawBody) > webBodyLimit {
		return webPage{}, webError("body_too_large", "page exceeds the 2 MiB body limit", false)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(rawBody)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return webPage{}, webError("unsupported_content", "invalid page content type", false)
	}
	if !strings.HasPrefix(mediaType, "text/") && mediaType != "application/xhtml+xml" && mediaType != "application/json" {
		return webPage{}, webError("unsupported_content", "page reader supports textual content; download binary documents through an authorized file workflow", false)
	}
	reader, err := charset.NewReader(bytes.NewReader(rawBody), contentType)
	if err != nil {
		return webPage{}, webError("decode_failed", "page character encoding could not be decoded", false)
	}
	body, err := io.ReadAll(io.LimitReader(reader, webBodyLimit+1))
	if err != nil {
		return webPage{}, err
	}
	if len(body) > webBodyLimit {
		return webPage{}, webError("body_too_large", "decoded page exceeds the 2 MiB body limit", false)
	}
	if !utf8.Valid(body) {
		return webPage{}, webError("decode_failed", "page is not valid decoded text", false)
	}
	page := webPage{URL: resp.Request.URL.String(), Text: strings.TrimSpace(string(body))}
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		page.Title, page.Text, err = readableWebHTML(string(body), resp.Request.URL)
		if err != nil {
			return webPage{}, webError("html_parse_failed", "page HTML could not be parsed", false)
		}
	}
	if page.Text == "" {
		return webPage{}, webError("empty_content", "page contains no readable text; it may require browser rendering or login", false)
	}
	if len(page.Text) > webBodyLimit {
		return webPage{}, webError("body_too_large", "extracted page exceeds the 2 MiB text limit", false)
	}
	return page, nil
}

func webFailure(err error) (string, string, bool) {
	var known *webReadError
	if errors.As(err, &known) {
		return known.code, known.message, known.retryable
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "tls_failed", "page TLS certificate could not be verified", false
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout", "page read timed out", true
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", "page read cancelled", false
	}
	return "connection_failed", "page connection or body read failed", true
}
