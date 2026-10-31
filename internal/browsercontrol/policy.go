package browsercontrol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Policy decides which addresses the driven browser may reach. It mirrors the
// web_fetch rule: public hosts are fine, private and special-use networks need
// an exact host the operator listed. It is applied to every navigation and,
// through request interception, to every sub-request a page makes, so a public
// page cannot use the browser to reach an intranet service.
//
// Hostnames are resolved here before the browser resolves them itself, so a
// name that changes its answer in between (DNS rebinding) is not fully covered;
// the allowance list is the real boundary for private hosts.
type Policy struct {
	// PrivateHosts holds lower-case exact hostnames or IP literals that may be
	// reached even when they are private.
	PrivateHosts map[string]bool
	// AllowLoopback opens 127.0.0.0/8 and ::1. Only tests set it.
	AllowLoopback bool
	// Lookup resolves a hostname; nil uses the system resolver.
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

	mu    sync.Mutex
	cache map[string]cachedLookup
}

type cachedLookup struct {
	ips []netip.Addr
	at  time.Time
}

const lookupTTL = 30 * time.Second

// ParsePrivateHosts reads a comma-separated list the way the flag is written.
func ParsePrivateHosts(list string) map[string]bool {
	out := map[string]bool{}
	for _, h := range strings.Split(list, ",") {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h != "" {
			out[h] = true
		}
	}
	return out
}

// CheckNavigation validates a URL the agent asked to open.
func (p *Policy) CheckNavigation(ctx context.Context, raw string) error {
	if len(raw) > 8192 {
		return errors.New("the URL is too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https pages can be opened, not %q", u.Scheme)
	}
	return p.checkHTTP(ctx, u)
}

// CheckRequest validates one request the page itself made. navigation is true
// for a top-level or frame document, which must be http(s); other requests may
// also be data: or blob: (inline images and fonts).
func (p *Policy) CheckRequest(ctx context.Context, raw string, navigation bool) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return errors.New("not a valid URL")
	}
	switch u.Scheme {
	case "http", "https":
		return p.checkHTTP(ctx, u)
	case "data", "blob":
		if navigation {
			return fmt.Errorf("a %s: document is not allowed", u.Scheme)
		}
		return nil
	case "about":
		if raw == "about:blank" {
			return nil
		}
	}
	return fmt.Errorf("scheme %q is not allowed", u.Scheme)
}

func (p *Policy) checkHTTP(ctx context.Context, u *url.URL) error {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return errors.New("the URL has no host")
	}
	if u.User != nil {
		return errors.New("URLs with embedded credentials are not allowed")
	}
	if p.PrivateHosts[host] {
		return nil
	}
	ips, err := p.resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("host %q could not be resolved", host)
	}
	for _, ip := range ips {
		if p.AllowLoopback && ip.IsLoopback() {
			continue
		}
		if !publicAddress(ip) {
			return fmt.Errorf("host %q is on a private or special-use network; the operator must list it in AGENTGO_BROWSER_PRIVATE_HOSTS first", host)
		}
	}
	return nil
}

func (p *Policy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	p.mu.Lock()
	if c, ok := p.cache[host]; ok && time.Since(c.at) < lookupTTL {
		p.mu.Unlock()
		return c.ips, nil
	}
	p.mu.Unlock()

	lookup := p.Lookup
	if lookup == nil {
		lookup = func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		}
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no addresses")
	}
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]cachedLookup{}
	}
	p.cache[host] = cachedLookup{ips: ips, at: time.Now()}
	p.mu.Unlock()
	return ips, nil
}

// The same special-use ranges web_fetch refuses; kept here so this package does
// not depend on the tools package.
var specialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("fec0::/10"), netip.MustParsePrefix("::/96"),
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range specialNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
