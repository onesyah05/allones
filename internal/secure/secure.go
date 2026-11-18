// Package secure holds the guardrails that sit between the model and the world:
// secret redaction, SSRF protection, engagement scope matching, dangerous-command
// detection, untrusted-content fencing, and an at-rest encryption vault.
package secure

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------- Secret redaction ----------

type rule struct {
	name string
	re   *regexp.Regexp
}

// SecretRules are high-signal patterns (subset inspired by gitleaks' default ruleset).
var SecretRules = []rule{
	{"aws-access-key", regexp.MustCompile(`\b(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,255}\b`)},
	{"github-fine-grained", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{60,255}\b`)},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9\-_]{20,}\b`)},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9\-_]{20,}\b`)},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9\-_]{20,}\b`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z\-]{10,}\b`)},
	{"slack-webhook", regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]{20,}`)},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{20,}\b`)},
	{"telegram-bot-token", regexp.MustCompile(`\b\d{8,10}:AA[0-9A-Za-z\-_]{33}\b`)},
	{"discord-token", regexp.MustCompile(`\b[MN][A-Za-z\d]{23,25}\.[\w-]{6}\.[\w-]{27,}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END (?:RSA |EC |DSA |OPENSSH |PGP |ENCRYPTED )?PRIVATE KEY(?: BLOCK)?-----`)},
	{"generic-assignment", regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)\b\s*[:=]\s*["']?([^\s"']{8,})`)},
	{"url-credentials", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:([^\s:/@]{3,})@`)},
}

// Redact masks secrets in text. The returned count tells how many were masked.
func Redact(s string) (string, int) {
	n := 0
	for _, r := range SecretRules {
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			if r.name == "generic-assignment" || r.name == "url-credentials" {
				sub := r.re.FindStringSubmatch(m)
				if len(sub) > 1 && sub[1] != "" {
					return strings.Replace(m, sub[1], "[REDACTED]", 1)
				}
			}
			keep := 4
			if len(m) <= 12 {
				keep = 0
			}
			return m[:keep] + "[REDACTED:" + r.name + "]"
		})
	}
	return s, n
}

type SecretHit struct {
	Rule   string `json:"rule"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line"`
	Match  string `json:"match"`
	Entropy float64 `json:"entropy"`
}

// ScanSecrets finds secrets line by line (excluding the over-broad generic rule unless entropy is high).
func ScanSecrets(text string) []SecretHit {
	var hits []SecretHit
	for i, line := range strings.Split(text, "\n") {
		if len(line) > 4000 {
			line = line[:4000]
		}
		for _, r := range SecretRules {
			for _, m := range r.re.FindAllString(line, 4) {
				val := m
				if sub := r.re.FindStringSubmatch(m); len(sub) > 1 && sub[1] != "" {
					val = sub[1]
				}
				e := Entropy(val)
				if r.name == "generic-assignment" && (e < 3.2 || looksPlaceholder(val)) {
					continue
				}
				masked := m
				if len(masked) > 12 {
					masked = masked[:8] + "…" + masked[len(masked)-3:]
				}
				hits = append(hits, SecretHit{Rule: r.name, Line: i + 1, Match: masked, Entropy: e})
			}
		}
	}
	return hits
}

func looksPlaceholder(v string) bool {
	l := strings.ToLower(v)
	for _, p := range []string{"example", "changeme", "your", "xxxx", "<", "${", "{{", "placeholder", "dummy", "test", "process.env", "os.getenv"} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// Entropy is the Shannon entropy in bits per character.
func Entropy(s string) float64 {
	if s == "" {
		return 0
	}
	freq := map[rune]float64{}
	for _, r := range s {
		freq[r]++
	}
	var e float64
	n := float64(len([]rune(s)))
	for _, c := range freq {
		p := c / n
		e -= p * log2(p)
	}
	return e
}

func log2(x float64) float64 {
	// small dependency-free log2 via change of base on math.Log would need math; keep exact:
	return ln(x) / 0.6931471805599453
}

func ln(x float64) float64 {
	// Use the standard library through a tiny indirection to keep imports tidy.
	return mathLog(x)
}

// ---------- Untrusted content ----------

// Fence wraps external content so the model treats it as data, not instructions
// (OWASP LLM01 / ASI01 indirect prompt injection mitigation).
func Fence(source, content string) string {
	content = strings.ReplaceAll(content, "</untrusted", "<\\/untrusted")
	return fmt.Sprintf("<untrusted source=%q>\n%s\n</untrusted>", source, content)
}

var injectionHints = regexp.MustCompile(`(?i)(ignore (all |any )?(previous|prior|above) (instructions|prompts)|disregard (the )?(system|previous)|you are now|new instructions:|system prompt:|<\s*/?\s*system\s*>|act as (an? )?(unfiltered|jailbroken)|exfiltrate|send (the|your) (api key|credentials|secrets))`)

// InjectionSuspect flags content that looks like it is trying to steer the agent.
func InjectionSuspect(s string) bool { return injectionHints.MatchString(s) }

// ---------- Dangerous commands ----------

type CommandGuard struct{ res []*regexp.Regexp }

func NewCommandGuard(patterns []string) *CommandGuard {
	g := &CommandGuard{}
	for _, p := range patterns {
		if re, err := regexp.Compile(`(?i)` + p); err == nil {
			g.res = append(g.res, re)
		}
	}
	return g
}

// Dangerous reports the first matching pattern, if any.
func (g *CommandGuard) Dangerous(cmd string) (string, bool) {
	for _, re := range g.res {
		if re.MatchString(cmd) {
			return re.String(), true
		}
	}
	return "", false
}

// ---------- SSRF ----------

var ErrSSRF = errors.New("blocked: destination resolves to a private, loopback or metadata address (security.ssrf_protect)")

// PrivateAddr reports loopback, private, link-local, CGNAT and metadata ranges.
func PrivateAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsUnspecified() || a.IsMulticast() {
		return true
	}
	for _, p := range []string{"100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "198.18.0.0/15", "fd00::/8", "fc00::/7"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
}

// SafeDialer returns a DialContext that refuses private destinations unless allow(host) is true.
// Resolution and the check happen at connect time, defeating DNS rebinding.
func SafeDialer(allow func(host string) bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if allow != nil && allow(host) {
			return d.DialContext(ctx, network, addr)
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if PrivateAddr(ip) {
				return nil, ErrSSRF
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no address for %s", host)
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}

// ---------- Scope ----------

// Scope matches hosts/URLs/IPs against an engagement's scope and exclusions.
// Entries: example.com (exact), *.example.com (subdomains), 10.0.0.0/24, 1.2.3.4, https://app.example.com/path
type Scope struct {
	Include, Exclude []string
}

func HostOf(target string) string {
	t := strings.TrimSpace(target)
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil {
			return strings.ToLower(u.Hostname())
		}
	}
	if h, _, err := net.SplitHostPort(t); err == nil {
		return strings.ToLower(h)
	}
	if i := strings.IndexAny(t, "/?#"); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(strings.Trim(t, "[]"))
}

func matchEntry(entry, host string) bool {
	e := strings.ToLower(strings.TrimSpace(entry))
	if e == "" {
		return false
	}
	if strings.Contains(e, "://") || strings.ContainsAny(e, "/") && !strings.Contains(e, ":") && !looksCIDR(e) {
		e = HostOf(e)
	}
	if looksCIDR(e) {
		pfx, err := netip.ParsePrefix(e)
		if err != nil {
			return false
		}
		if a, err := netip.ParseAddr(host); err == nil {
			return pfx.Contains(a.Unmap())
		}
		return false
	}
	if strings.HasPrefix(e, "*.") {
		base := e[2:]
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == e
}

func looksCIDR(s string) bool {
	_, err := netip.ParsePrefix(s)
	return err == nil
}

// Check returns (inScope, reason).
func (s Scope) Check(target string) (bool, string) {
	host := HostOf(target)
	if host == "" {
		return false, "no host"
	}
	for _, e := range s.Exclude {
		if matchEntry(e, host) {
			return false, "excluded by " + e
		}
	}
	if len(s.Include) == 0 {
		return false, "engagement has no scope defined"
	}
	for _, e := range s.Include {
		if matchEntry(e, host) {
			return true, "matches " + e
		}
	}
	return false, "not in scope"
}

// ---------- Vault (AES-256-GCM, key in ~/.allones/master.key) ----------

type Vault struct{ aead cipher.AEAD }

func OpenVault(home string) (*Vault, error) {
	path := filepath.Join(home, "master.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must hold 32 bytes", path)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := v.aead.Seal(nonce, nonce, []byte(plain), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(ct), nil
}

func (v *Vault) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	if err != nil {
		return "", err
	}
	ns := v.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	pt, err := v.aead.Open(nil, raw[:ns], raw[ns:], nil)
	return string(pt), err
}
