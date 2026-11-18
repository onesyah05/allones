package tools

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/secure"
	"golang.org/x/net/html"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// HTTPClient returns a client that honours SSRF protection. Hosts in the active
// engagement scope may be private (that is the point of an internal test).
func HTTPClient(env *Env, timeout time.Duration, follow bool, insecure bool) *http.Client {
	allow := func(host string) bool {
		if !env.Cfg.Security.SSRFProtect {
			return true
		}
		if eng := env.Engagement(); eng != nil {
			ok, _ := secure.Scope{Include: eng.Scope, Exclude: eng.Exclusions}.Check(host)
			return ok
		}
		return false
	}
	tr := &http.Transport{
		DialContext:         secure.SafeDialer(allow),
		TLSHandshakeTimeout: 15 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // opt-in for testing self-signed targets
		MaxIdleConns:        20,
		IdleConnTimeout:     30 * time.Second,
		Proxy:               http.ProxyFromEnvironment,
	}
	c := &http.Client{Timeout: timeout, Transport: tr}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	} else {
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return fmt.Errorf("stopped after 8 redirects")
			}
			return nil
		}
	}
	return c
}

func registerWeb(r *Registry) {
	r.Add(&Tool{Name: "web_search", Category: "web",
		Description: "Search the web. Returns ranked results with title, URL and snippet. Follow up with web_fetch to read a page.",
		Schema:      Obj(map[string]any{"query": Str("Search query."), "count": Int("Results, default 8 (max 20).")}, "query"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Query string
				Count int
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			if a.Count <= 0 || a.Count > 20 {
				a.Count = 8
			}
			res, err := Search(ctx, env, a.Query, a.Count)
			if err != nil {
				return Errorf("search failed: %v", err)
			}
			if len(res) == 0 {
				return Text("no results")
			}
			var b strings.Builder
			for i, x := range res {
				fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, x.Title, x.URL, x.Snippet)
			}
			return External("web_search", b.String())
		}})

	r.Add(&Tool{Name: "web_fetch", Category: "web",
		Description: "Fetch a URL and return readable text (HTML is converted, scripts removed; JSON is pretty-printed). Faster than browser; use browser for pages that need JavaScript or login.",
		Schema:      Obj(map[string]any{"url": Str("http(s) URL."), "max_chars": Int("Limit on returned characters (default 40000)."), "raw": Bool("Return raw body instead of extracted text.")}, "url"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				URL      string
				MaxChars int `json:"max_chars"`
				Raw      bool
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			u, err := url.Parse(a.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return Errorf("need an http(s) URL")
			}
			if a.MaxChars <= 0 {
				a.MaxChars = 40000
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
			req.Header.Set("User-Agent", browserUA)
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9,id;q=0.8")
			resp, err := HTTPClient(env, 45*time.Second, true, false).Do(req)
			if err != nil {
				return Errorf("fetch: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 6<<20))
			ct := resp.Header.Get("Content-Type")
			var text string
			switch {
			case a.Raw:
				text = string(body)
			case strings.Contains(ct, "json"):
				var buf bytes.Buffer
				if json.Indent(&buf, body, "", "  ") == nil {
					text = buf.String()
				} else {
					text = string(body)
				}
			case strings.Contains(ct, "html") || bytes.Contains(bytes.ToLower(body[:min(len(body), 512)]), []byte("<html")):
				text = HTMLToText(body, resp.Request.URL)
			case strings.HasPrefix(ct, "text/") || ct == "" || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript"):
				text = string(body)
			default:
				return Text(fmt.Sprintf("HTTP %d, %s, %d bytes (binary; not shown). Use shell (curl -o) to download it.", resp.StatusCode, ct, len(body)))
			}
			hdr := fmt.Sprintf("URL: %s\nStatus: %d\nContent-Type: %s\n\n", resp.Request.URL, resp.StatusCode, ct)
			return Result{Text: Truncate(hdr+text, a.MaxChars), Untrusted: resp.Request.URL.Host, IsError: resp.StatusCode >= 400}
		}})

	r.Add(&Tool{Name: "http_request", Category: "web", Mutates: true, Network: true,
		Description: "Send a raw HTTP request (any method, headers, body) and see the full response: status, headers, body. For API testing, auth flows and security verification. Does not follow redirects unless follow=true. Scope-checked during an engagement.",
		Schema: Obj(map[string]any{"url": Str("Target URL."), "method": Str("HTTP method, default GET."), "headers": map[string]any{"type": "object", "description": "Request headers.", "additionalProperties": map[string]any{"type": "string"}},
			"body": Str("Request body."), "follow": Bool("Follow redirects."), "insecure": Bool("Skip TLS verification (self-signed test targets)."), "timeout": Int("Seconds, default 30.")}, "url"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				URL, Method, Body string
				Headers           map[string]string
				Follow, Insecure  bool
				Timeout           int
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			warn, err := env.CheckTarget(ctx, "http_request", a.URL)
			if err != nil {
				return Errorf("%v", err)
			}
			if a.Method == "" {
				a.Method = "GET"
			}
			if a.Timeout <= 0 {
				a.Timeout = 30
			}
			req, err := http.NewRequestWithContext(ctx, strings.ToUpper(a.Method), a.URL, strings.NewReader(a.Body))
			if err != nil {
				return Errorf("%v", err)
			}
			req.Header.Set("User-Agent", browserUA)
			for k, v := range a.Headers {
				if strings.EqualFold(k, "host") {
					req.Host = v
					continue
				}
				req.Header.Set(k, v)
			}
			start := time.Now()
			resp, err := HTTPClient(env, time.Duration(a.Timeout)*time.Second, a.Follow, a.Insecure).Do(req)
			if err != nil {
				return Errorf("request failed: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			var b strings.Builder
			if warn != "" {
				b.WriteString(warn + "\n\n")
			}
			fmt.Fprintf(&b, "%s %s\n", resp.Proto, resp.Status)
			keys := make([]string, 0, len(resp.Header))
			for k := range resp.Header {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				for _, v := range resp.Header[k] {
					fmt.Fprintf(&b, "%s: %s\n", k, v)
				}
			}
			fmt.Fprintf(&b, "\n[%d bytes in %s]\n", len(body), time.Since(start).Round(time.Millisecond))
			b.Write(body)
			return Result{Text: Truncate(b.String(), env.Cfg.Tools.MaxOutputChars), Untrusted: req.URL.Host,
				Meta: map[string]any{"status": resp.StatusCode, "bytes": len(body)}}
		}})
}

type SearchResult struct {
	Title, URL, Snippet string
}

func Search(ctx context.Context, env *Env, q string, n int) ([]SearchResult, error) {
	cfg := env.Cfg.Tools.Search
	hc := &http.Client{Timeout: 25 * time.Second}
	switch cfg.Provider {
	case "brave":
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.search.brave.com/res/v1/web/search?count="+fmt.Sprint(n)+"&q="+url.QueryEscape(q), nil)
		req.Header.Set("X-Subscription-Token", cfg.APIKey)
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var r struct {
			Web struct {
				Results []struct{ Title, URL, Description string }
			}
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return nil, err
		}
		var out []SearchResult
		for _, x := range r.Web.Results {
			out = append(out, SearchResult{x.Title, x.URL, stripTags(x.Description)})
		}
		return out, nil
	case "tavily":
		b, _ := json.Marshal(map[string]any{"api_key": cfg.APIKey, "query": q, "max_results": n})
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var r struct {
			Results []struct{ Title, URL, Content string }
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return nil, err
		}
		var out []SearchResult
		for _, x := range r.Results {
			out = append(out, SearchResult{x.Title, x.URL, clip(x.Content, 400)})
		}
		return out, nil
	case "searxng":
		req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(cfg.BaseURL, "/")+"/search?format=json&q="+url.QueryEscape(q), nil)
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var r struct {
			Results []struct{ Title, URL, Content string }
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return nil, err
		}
		var out []SearchResult
		for i, x := range r.Results {
			if i >= n {
				break
			}
			out = append(out, SearchResult{x.Title, x.URL, x.Content})
		}
		return out, nil
	}
	return duckduckgo(ctx, q, n)
}

func duckduckgo(ctx context.Context, q string, n int) ([]SearchResult, error) {
	form := url.Values{"q": {q}, "kl": {"wt-wt"}}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://html.duckduckgo.com/html/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", browserUA)
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	doc, err := html.Parse(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var out []SearchResult
	var cur *SearchResult
	var walk func(*html.Node)
	walk = func(nd *html.Node) {
		if nd.Type == html.ElementNode {
			cls := attr(nd, "class")
			switch {
			case nd.Data == "a" && strings.Contains(cls, "result__a"):
				href := attr(nd, "href")
				if u, err := url.Parse(href); err == nil {
					if real := u.Query().Get("uddg"); real != "" {
						href = real
					}
				}
				if strings.Contains(href, "duckduckgo.com/y.js") {
					break // ad
				}
				out = append(out, SearchResult{Title: strings.TrimSpace(textOf(nd)), URL: href})
				cur = &out[len(out)-1]
			case strings.Contains(cls, "result__snippet") && cur != nil:
				cur.Snippet = strings.TrimSpace(textOf(nd))
			}
		}
		for c := nd.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func stripTags(s string) string {
	n, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}
	return textOf(n)
}

// HTMLToText renders HTML into compact Markdown-ish text with links.
func HTMLToText(body []byte, base *url.URL) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	var b strings.Builder
	title := ""
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "template": true, "iframe": true, "head": false}
	var walk func(*html.Node, int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			if skip[n.Data] {
				return
			}
			if n.Data == "title" && title == "" {
				title = textOf(n)
				return
			}
			switch n.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteString("\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " " + textOf(n) + "\n")
				return
			case "p", "div", "section", "article", "header", "footer", "main", "tr", "table", "form", "ul", "ol":
				b.WriteString("\n")
			case "br":
				b.WriteString("\n")
			case "li":
				b.WriteString("\n- ")
			case "td", "th":
				b.WriteString(" | ")
			case "pre", "code":
				if n.Data == "pre" {
					b.WriteString("\n```\n" + textRaw(n) + "\n```\n")
					return
				}
			case "a":
				href := attr(n, "href")
				t := textOf(n)
				if href != "" && !strings.HasPrefix(href, "javascript:") && t != "" {
					if base != nil {
						if u, err := base.Parse(href); err == nil {
							href = u.String()
						}
					}
					b.WriteString("[" + t + "](" + href + ")")
					return
				}
			case "input", "textarea", "select", "button":
				nm := attr(n, "name")
				if nm != "" || n.Data == "button" {
					b.WriteString(fmt.Sprintf(" [%s name=%q type=%q] ", n.Data, nm, attr(n, "type")))
				}
			case "img":
				if alt := attr(n, "alt"); alt != "" {
					b.WriteString("[image: " + alt + "]")
				}
			}
		} else if n.Type == html.TextNode {
			t := strings.Join(strings.Fields(n.Data), " ")
			if t != "" {
				b.WriteString(t + " ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth+1)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div") {
			b.WriteString("\n")
		}
	}
	walk(doc, 0)
	lines := strings.Split(b.String(), "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	s := strings.TrimSpace(strings.Join(out, "\n"))
	if title != "" {
		s = "Title: " + title + "\n\n" + s
	}
	return s
}

func textRaw(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return b.String()
}

var _ = net.ParseIP
