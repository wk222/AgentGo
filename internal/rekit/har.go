package rekit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
)

type harNV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harEntry struct {
	Started string  `json:"startedDateTime"`
	Time    float64 `json:"time"`
	Request struct {
		Method      string  `json:"method"`
		URL         string  `json:"url"`
		Headers     []harNV `json:"headers"`
		QueryString []harNV `json:"queryString"`
		PostData    *struct {
			MimeType string  `json:"mimeType"`
			Text     string  `json:"text"`
			Params   []harNV `json:"params"`
		} `json:"postData"`
	} `json:"request"`
	Response struct {
		Status  int     `json:"status"`
		Headers []harNV `json:"headers"`
		Content struct {
			Size     int64  `json:"size"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Encoding string `json:"encoding"`
		} `json:"content"`
	} `json:"response"`
}

// HAR is a parsed capture.
type HAR struct{ Entries []harEntry }

// LoadHAR parses a HAR (or Fiddler-exported HAR) file.
func LoadHAR(path string) (*HAR, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // Fiddler/Windows exports often carry a UTF-8 BOM
	var doc struct {
		Log struct {
			Entries []harEntry `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("not a HAR file: %w", err)
	}
	return &HAR{Entries: doc.Log.Entries}, nil
}

// HARFilter selects entries.
type HARFilter struct {
	HostContains string
	PathRegex    string
	Method       string
	Status       int
	JSONOnly     bool
}

// Endpoint is one grouped METHOD host/path row.
type Endpoint struct {
	Key             string      `json:"endpoint"`
	Count           int         `json:"count"`
	Entries         []int       `json:"entry_indexes"`
	Statuses        map[int]int `json:"statuses"`
	QueryKeys       []string    `json:"query_keys,omitempty"`
	ReqType         string      `json:"request_content_type,omitempty"`
	ReqBodyKeys     []string    `json:"request_body_keys,omitempty"`
	RespType        string      `json:"response_content_type,omitempty"`
	RespBodyKeys    []string    `json:"response_body_keys,omitempty"`
	InterestingHdrs []string    `json:"auth_or_sign_headers,omitempty"`
	AvgRespBytes    int64       `json:"avg_response_bytes"`
	sumResp         int64
	qk, rk, sk, ih  map[string]bool
}

var (
	idSeg     = regexp.MustCompile(`^(\d{5,}|[0-9a-fA-F]{16,}|[0-9a-fA-F-]{32,36})$`)
	signHdrRe = regexp.MustCompile(`(?i)sign|token|nonce|timestamp|auth|session|common|param|cookie|key|^ts$|^x-|^accountid`)
	secretHdr = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|cookie|set-cookie|x-api-key|x-auth-token|auth-token)$`)
)

func normPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if idSeg.MatchString(s) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

func jsonKeys(text string, depth int) []string {
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return nil
	}
	set := map[string]bool{}
	var walk func(prefix string, v any, d int)
	walk = func(prefix string, v any, d int) {
		switch t := v.(type) {
		case map[string]any:
			for k, c := range t {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				set[p] = true
				if d < depth {
					walk(p, c, d+1)
				}
			}
		case []any:
			if len(t) > 0 && d < depth {
				walk(prefix+"[]", t[0], d+1)
			}
		}
	}
	walk("", v, 1)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func respText(e *harEntry) string {
	c := e.Response.Content
	if c.Encoding == "base64" {
		if b, err := base64.StdEncoding.DecodeString(c.Text); err == nil {
			return string(b)
		}
	}
	return c.Text
}

func (f HARFilter) match(e *harEntry) (*url.URL, bool) {
	u, err := url.Parse(e.Request.URL)
	if err != nil {
		return nil, false
	}
	if f.HostContains != "" && !strings.Contains(strings.ToLower(u.Host), strings.ToLower(f.HostContains)) {
		return nil, false
	}
	if f.Method != "" && !strings.EqualFold(f.Method, e.Request.Method) {
		return nil, false
	}
	if f.Status != 0 && e.Response.Status != f.Status {
		return nil, false
	}
	if f.PathRegex != "" {
		if ok, _ := regexp.MatchString(f.PathRegex, u.Path); !ok {
			return nil, false
		}
	}
	if f.JSONOnly && !strings.Contains(strings.ToLower(e.Response.Content.MimeType), "json") {
		return nil, false
	}
	return u, true
}

// Summarize groups matching entries into endpoints (the "HAR-first" reverse view).
func (h *HAR) Summarize(f HARFilter) []*Endpoint {
	idx := map[string]*Endpoint{}
	var order []*Endpoint
	for i := range h.Entries {
		e := &h.Entries[i]
		u, ok := f.match(e)
		if !ok {
			continue
		}
		key := e.Request.Method + " " + u.Host + normPath(u.Path)
		ep := idx[key]
		if ep == nil {
			ep = &Endpoint{Key: key, Statuses: map[int]int{}, qk: map[string]bool{}, rk: map[string]bool{}, sk: map[string]bool{}, ih: map[string]bool{}}
			idx[key] = ep
			order = append(order, ep)
		}
		ep.Count++
		if len(ep.Entries) < 8 {
			ep.Entries = append(ep.Entries, i)
		}
		ep.Statuses[e.Response.Status]++
		ep.sumResp += e.Response.Content.Size
		for k := range u.Query() {
			ep.qk[k] = true
		}
		for _, hd := range e.Request.Headers {
			if signHdrRe.MatchString(hd.Name) && !strings.HasPrefix(hd.Name, ":") {
				ep.ih[strings.ToLower(hd.Name)] = true
			}
		}
		if pd := e.Request.PostData; pd != nil {
			ep.ReqType = pd.MimeType
			for _, p := range pd.Params {
				ep.rk[p.Name] = true
			}
			for _, k := range jsonKeys(pd.Text, 2) {
				ep.rk[k] = true
			}
			if len(pd.Params) == 0 && !strings.Contains(pd.MimeType, "json") && strings.Contains(pd.Text, "=") {
				if vals, err := url.ParseQuery(pd.Text); err == nil {
					for k := range vals {
						ep.rk[k] = true
					}
				}
			}
		}
		ep.RespType = e.Response.Content.MimeType
		for _, k := range jsonKeys(respText(e), 2) {
			ep.sk[k] = true
		}
	}
	for _, ep := range order {
		ep.AvgRespBytes = ep.sumResp / int64(max(ep.Count, 1))
		ep.QueryKeys, ep.ReqBodyKeys, ep.RespBodyKeys, ep.InterestingHdrs = keys(ep.qk), keys(ep.rk), keys(ep.sk), keys(ep.ih)
	}
	return order
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// EntryDetail is one request/response with credentials masked unless reveal is set.
type EntryDetail struct {
	Index        int               `json:"index"`
	Started      string            `json:"started"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	ReqHeaders   map[string]string `json:"request_headers"`
	ReqBody      string            `json:"request_body,omitempty"`
	Status       int               `json:"status"`
	RespHeaders  map[string]string `json:"response_headers"`
	RespBody     string            `json:"response_body,omitempty"`
	BodyTrimmed  bool              `json:"body_trimmed,omitempty"`
	MaskedHeader []string          `json:"masked_headers,omitempty"`
}

func clip(s string, n int) (string, bool) {
	if n <= 0 || len(s) <= n {
		return s, false
	}
	return s[:n] + fmt.Sprintf("…[+%d bytes]", len(s)-n), true
}

// Detail returns entry i. Authorization/Cookie-class header values are masked by default
// so a token never lands in the model context unless the caller asks (reveal=true).
func (h *HAR) Detail(i, maxBody int, reveal bool) (*EntryDetail, error) {
	if i < 0 || i >= len(h.Entries) {
		return nil, fmt.Errorf("entry %d out of range (0..%d)", i, len(h.Entries)-1)
	}
	e := &h.Entries[i]
	d := &EntryDetail{Index: i, Started: e.Started, Method: e.Request.Method, URL: e.Request.URL, Status: e.Response.Status,
		ReqHeaders: map[string]string{}, RespHeaders: map[string]string{}}
	mask := func(dst map[string]string, hs []harNV) {
		for _, hd := range hs {
			if strings.HasPrefix(hd.Name, ":") {
				continue
			}
			v := hd.Value
			if !reveal && secretHdr.MatchString(hd.Name) {
				d.MaskedHeader = append(d.MaskedHeader, hd.Name)
				v = fmt.Sprintf("%.6s…(len %d, masked)", v, len(hd.Value))
			}
			if old, ok := dst[hd.Name]; ok {
				v = old + "; " + v
			}
			dst[hd.Name] = v
		}
	}
	mask(d.ReqHeaders, e.Request.Headers)
	mask(d.RespHeaders, e.Response.Headers)
	var t1, t2 bool
	if pd := e.Request.PostData; pd != nil {
		d.ReqBody, t1 = clip(pd.Text, maxBody)
	}
	d.RespBody, t2 = clip(respText(e), maxBody)
	d.BodyTrimmed = t1 || t2
	return d, nil
}
