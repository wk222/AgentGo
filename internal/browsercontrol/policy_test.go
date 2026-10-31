package browsercontrol

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func fixedLookup(table map[string]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, h string) ([]netip.Addr, error) {
		s, ok := table[h]
		if !ok {
			return nil, errors.New("nxdomain")
		}
		var out []netip.Addr
		for _, p := range strings.Split(s, ",") {
			out = append(out, netip.MustParseAddr(p))
		}
		return out, nil
	}
}

func TestNavigationRefusesWhatAnAgentMustNotReach(t *testing.T) {
	p := &Policy{Lookup: fixedLookup(map[string]string{
		"example.com":      "93.184.216.34",
		"intranet.corp":    "10.1.2.3",
		"mixed.example":    "93.184.216.34,10.0.0.5",
		"rebind.example":   "127.0.0.1",
		"meta.example":     "169.254.169.254",
		"v6mapped.example": "::ffff:10.0.0.1",
		"shared.example":   "100.64.0.9",
	})}
	cases := []struct {
		url  string
		deny string // substring of the refusal; "" = allowed
	}{
		{"https://example.com/a?b=1", ""},
		{"http://example.com", ""},
		{"http://93.184.216.34/", ""},
		{"file:///C:/Windows/win.ini", "only http and https"},
		{"javascript:alert(1)", "only http and https"},
		{"data:text/html,<b>x</b>", "only http and https"},
		{"chrome://settings", "only http and https"},
		{"ftp://example.com/x", "only http and https"},
		{"http://127.0.0.1:8788/", "private or special-use"},
		{"http://[::1]/", "private or special-use"},
		{"http://localhost/", "could not be resolved"}, // not in the fake table
		{"http://10.0.0.1/", "private or special-use"},
		{"http://192.168.1.1/", "private or special-use"},
		{"http://172.16.0.1/", "private or special-use"},
		{"http://169.254.169.254/latest/meta-data", "private or special-use"},
		{"http://100.64.0.1/", "private or special-use"},
		{"http://[::ffff:127.0.0.1]/", "private or special-use"},
		{"http://[fec0::1]/", "private or special-use"},
		{"http://intranet.corp/oa", "private or special-use"},
		{"http://mixed.example/", "private or special-use"}, // one bad answer is enough
		{"http://rebind.example/", "private or special-use"},
		{"http://meta.example/", "private or special-use"},
		{"http://v6mapped.example/", "private or special-use"},
		{"http://shared.example/", "private or special-use"},
		{"http://user:pw@example.com/", "credentials"},
		{"http:///nohost", "no host"},
		{"http://nonexistent.example/", "could not be resolved"},
	}
	for _, c := range cases {
		err := p.CheckNavigation(context.Background(), c.url)
		switch {
		case c.deny == "" && err != nil:
			t.Errorf("%s was refused: %v", c.url, err)
		case c.deny != "" && err == nil:
			t.Errorf("%s was allowed", c.url)
		case c.deny != "" && !strings.Contains(err.Error(), c.deny):
			t.Errorf("%s: refusal %q does not mention %q", c.url, err, c.deny)
		}
	}
}

func TestAnOperatorListedPrivateHostIsReachableButOnlyThatHost(t *testing.T) {
	p := &Policy{
		PrivateHosts: ParsePrivateHosts(" Intranet.Corp. , 10.9.9.9 ,, "),
		Lookup:       fixedLookup(map[string]string{"intranet.corp": "10.1.2.3", "other.corp": "10.1.2.4"}),
	}
	for _, u := range []string{"http://intranet.corp/oa", "http://INTRANET.CORP./x", "http://10.9.9.9:8080/"} {
		if err := p.CheckNavigation(context.Background(), u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://other.corp/", "http://10.9.9.8/", "http://sub.intranet.corp/"} {
		if err := p.CheckNavigation(context.Background(), u); err == nil {
			t.Errorf("%s was allowed; the allowance is for exact hosts, not neighbours or subdomains", u)
		}
	}
}

func TestSubRequestsFollowTheSameRulesPlusInlineSchemes(t *testing.T) {
	p := &Policy{Lookup: fixedLookup(map[string]string{"cdn.example": "93.184.216.34"})}
	ctx := context.Background()
	for _, u := range []string{"https://cdn.example/app.js", "data:image/png;base64,AAAA", "blob:https://cdn.example/uuid", "about:blank"} {
		if err := p.CheckRequest(ctx, u, false); err != nil {
			t.Errorf("sub-request %s: %v", u, err)
		}
	}
	for _, u := range []string{"http://10.0.0.1/admin", "file:///etc/passwd", "ftp://cdn.example/", "about:srcdoc", "chrome-extension://x/y"} {
		if err := p.CheckRequest(ctx, u, false); err == nil {
			t.Errorf("sub-request %s was allowed", u)
		}
	}
	// A document (top-level or frame) may not be a data or blob URL.
	for _, u := range []string{"data:text/html,<script>1</script>", "blob:https://cdn.example/uuid"} {
		if err := p.CheckRequest(ctx, u, true); err == nil {
			t.Errorf("document %s was allowed", u)
		}
	}
	if err := p.CheckRequest(ctx, "about:blank", true); err != nil {
		t.Errorf("about:blank document: %v", err)
	}
}

func TestLoopbackIsOnlyOpenWhenAskedFor(t *testing.T) {
	closed := &Policy{}
	open := &Policy{AllowLoopback: true}
	if closed.CheckNavigation(context.Background(), "http://127.0.0.1:9/") == nil {
		t.Fatal("loopback allowed by default")
	}
	if err := open.CheckNavigation(context.Background(), "http://127.0.0.1:9/"); err != nil {
		t.Fatal(err)
	}
	if open.CheckNavigation(context.Background(), "http://10.0.0.1/") == nil {
		t.Fatal("opening loopback must not open the rest of the private network")
	}
}

func TestResolutionIsCachedBriefly(t *testing.T) {
	calls := 0
	p := &Policy{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		calls++
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}}
	for i := 0; i < 5; i++ {
		if err := p.CheckNavigation(context.Background(), "https://example.com/p"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("%d lookups for one host", calls)
	}
}
