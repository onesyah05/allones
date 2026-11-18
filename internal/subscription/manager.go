package subscription

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
)

// Status is safe for UI responses. It never contains tokens or a PKCE verifier.
type Status struct {
	Phase    string `json:"phase"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	UserCode string `json:"user_code"`
	Message  string `json:"message"`
}
type flow struct {
	status Status
	cancel context.CancelFunc
}
type Manager struct {
	mu        sync.Mutex
	flows     map[string]*flow
	cfg       *config.Manager
	client    *http.Client
	endpoints endpoints
	lifetime  time.Duration
	pollUnit  time.Duration
	// persist is injectable for tests; production always uses cfg.Update.
	persist func(string, Tokens) error
}

func New(cfg *config.Manager) *Manager {
	m := &Manager{cfg: cfg, flows: make(map[string]*flow), client: &http.Client{Timeout: 30 * time.Second}, endpoints: defaults(), lifetime: 15 * time.Minute, pollUnit: time.Second}
	m.persist = m.save
	return m
}
func (m *Manager) save(kind string, t Tokens) error {
	if m.cfg == nil {
		return errors.New("configuration is unavailable")
	}
	return m.cfg.Update(func(c *config.Config) error {
		p := config.Provider{Name: kind, Kind: kind}
		idx := -1
		for i := range c.Provider {
			if c.Provider[i].Name == kind {
				idx = i
				p = c.Provider[i]
				break
			}
		}
		p.Name = kind
		p.Kind = kind
		if len(p.Models) == 0 {
			switch kind {
			case "codex":
				p.Models = []string{"gpt-6-sol", "gpt-6.1-sol", "gpt-6-astra"}
			case "claude-code":
				p.Models = []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"}
			case "antigravity":
				p.Models = []string{"gemini-3-flash", "gemini-3-pro-high", "claude-sonnet-4-6"}
			}
		}
		p.APIKey = t.Access
		p.RefreshToken = t.Refresh
		p.ExpiresAt = t.ExpiresAt
		p.OAuth = true
		p.Disabled = false
		p.FromEnv = false
		p.Headers = copyHeaders(p.Headers)
		if t.AccountID != "" {
			p.Headers["ChatGPT-Account-Id"] = t.AccountID
		}
		if idx < 0 {
			c.Provider = append(c.Provider, p)
		} else {
			c.Provider[idx] = p
		}
		return nil
	})
}

// Start returns immediately; the UI polls Status for the authorization URL/code.
// One active login per kind avoids competing callbacks on registered ports.
func (m *Manager) Start(kind string) (Status, error) {
	e, ok := m.endpoints.providers[kind]
	if !ok {
		return Status{}, errors.New("unsupported subscription kind")
	}
	id, err := randomString()
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.flows {
		if f.status.Kind == kind && !terminal(f.status.Phase) {
			return Status{}, errors.New("login already active for this kind")
		}
	}
	// Retain recent completions for polling without growing indefinitely.
	if len(m.flows) >= 128 {
		for key, f := range m.flows {
			if terminal(f.status.Phase) {
				delete(m.flows, key)
			}
		}
	}
	duration := m.lifetime
	if duration <= 0 || duration > 15*time.Minute {
		duration = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	s := Status{ID: id, Kind: kind, Phase: "starting", Message: "Starting login"}
	m.flows[id] = &flow{status: s, cancel: cancel}
	go m.run(ctx, id, kind, e)
	return s, nil
}
func terminal(phase string) bool { return phase == "done" || phase == "error" || phase == "cancelled" }
func (m *Manager) Status(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[id]
	if !ok {
		return Status{}, false
	}
	return f.status, true
}
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.flows[id]
	if !ok || terminal(f.status.Phase) {
		return false
	}
	f.status.Phase = "cancelled"
	f.status.Message = "Login cancelled"
	f.status.URL = ""
	f.status.UserCode = ""
	f.cancel()
	return true
}
func (m *Manager) emit(id, phase, message, link, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.flows[id]
	if f == nil || terminal(f.status.Phase) {
		return
	}
	f.status.Phase = phase
	f.status.Message = message
	f.status.URL = link
	f.status.UserCode = code
}
func (m *Manager) run(ctx context.Context, id, kind string, e endpoint) {
	defer func() {
		m.mu.Lock()
		if f := m.flows[id]; f != nil {
			f.cancel()
		}
		m.mu.Unlock()
	}()
	var t Tokens
	var err error
	if kind == "codex" {
		t, err = m.device(ctx, id, e)
	} else {
		t, err = m.browser(ctx, id, kind, e)
	}
	// Lock completion against Cancel: cancellation cannot win and still persist credentials.
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.flows[id]
	if f == nil || terminal(f.status.Phase) {
		return
	}
	if ctx.Err() != nil {
		err = errors.New("Login timed out")
	}
	if err == nil {
		err = m.persist(kind, t)
	}
	f.status.URL = ""
	f.status.UserCode = ""
	if err != nil {
		f.status.Phase = "error"
		if ctx.Err() != nil {
			f.status.Message = "Login timed out"
		} else {
			// Surface the real reason; these errors carry no tokens or secrets.
			f.status.Message = "Login failed: " + err.Error()
		}
	} else {
		f.status.Phase = "done"
		f.status.Message = "Subscription connected"
	}
}
func (m *Manager) device(ctx context.Context, id string, e endpoint) (Tokens, error) {
	var start struct {
		ID       string    `json:"device_auth_id"`
		Code     string    `json:"user_code"`
		Interval flexFloat `json:"interval"` // the endpoint sends this as a string
	}
	_, err := request(ctx, m.client, m.endpoints.deviceStart, nil, map[string]string{"client_id": e.clientID}, false, &start)
	if err != nil {
		return Tokens{}, err
	}
	if start.ID == "" || start.Code == "" {
		return Tokens{}, errors.New("invalid device authorization")
	}
	interval := float64(start.Interval)
	if interval <= 0 {
		interval = 5
	}
	if interval > 900 {
		interval = 900
	}
	m.emit(id, "awaiting_user", "Open the URL and enter the user code", m.endpoints.verification, start.Code)
	for {
		timer := time.NewTimer(time.Duration(interval * float64(m.pollUnit)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return Tokens{}, ctx.Err()
		case <-timer.C:
		}
		var poll struct {
			Code     string `json:"authorization_code"`
			Verifier string `json:"code_verifier"`
		}
		status, err := request(ctx, m.client, m.endpoints.devicePoll, nil, map[string]string{"device_auth_id": start.ID, "user_code": start.Code}, false, &poll)
		if status == 403 || status == 404 {
			continue
		}
		if err != nil {
			return Tokens{}, err
		}
		if poll.Code == "" || poll.Verifier == "" {
			continue
		}
		m.emit(id, "exchanging", "Exchanging authorization code", "", "")
		return exchange(ctx, m.client, e, "codex", url.Values{"grant_type": {"authorization_code"}, "code": {poll.Code}, "code_verifier": {poll.Verifier}, "redirect_uri": {e.redirect}}, true)
	}
}
func (m *Manager) browser(ctx context.Context, id, kind string, e endpoint) (Tokens, error) {
	verifier, err := randomString()
	if err != nil {
		return Tokens{}, err
	}
	state, err := randomString()
	if err != nil {
		return Tokens{}, err
	}
	redirect, err := url.Parse(e.redirect)
	if err != nil {
		return Tokens{}, err
	}
	// localhost is retained verbatim in redirect_uri, but only IPv4 loopback is bound.
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", redirect.Port()))
	if err != nil {
		return Tokens{}, err
	}
	result := make(chan string, 1)
	var consume sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path != redirect.Path {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		states := q["state"]
		// Validate before consuming the one-shot callback, including error callbacks.
		if len(states) != 1 || subtle.ConstantTimeCompare([]byte(states[0]), []byte(state)) != 1 {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		code := ""
		codes := q["code"]
		if len(codes) == 1 && q.Get("error") == "" {
			code = codes[0]
		}
		accepted := false
		consume.Do(func() { accepted = true; result <- code })
		if !accepted {
			http.Error(w, "Callback already consumed", http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte("Login received. Return to AllOnes."))
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second}
	defer server.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	digest := sha256.Sum256([]byte(verifier))
	u, err := url.Parse(e.authorize)
	if err != nil {
		return Tokens{}, err
	}
	q := u.Query()
	q.Set("client_id", e.clientID)
	q.Set("redirect_uri", e.redirect)
	q.Set("response_type", "code")
	q.Set("scope", e.scope)
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	q.Set("code_challenge_method", "S256")
	if kind == "antigravity" {
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
	u.RawQuery = q.Encode()
	m.emit(id, "awaiting_user", "Complete login in your browser", u.String(), "")
	var code string
	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case err := <-serveErr:
		return Tokens{}, err
	case code = <-result:
	}
	if code == "" {
		return Tokens{}, errors.New("authorization rejected or missing code")
	}
	m.emit(id, "exchanging", "Exchanging authorization code", "", "")
	return exchange(ctx, m.client, e, kind, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {e.redirect}}, true)
}
