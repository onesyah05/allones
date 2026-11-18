package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/allones/allones/internal/config"
)

func await(t *testing.T, m *Manager, id, phase string) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := m.Status(id)
		if s.Phase == phase {
			return s
		}
		if terminal(s.Phase) {
			t.Fatalf("wanted %s, got %+v", phase, s)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("status timeout")
	return Status{}
}
func freeRedirect(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	l.Close()
	return "http://" + address + "/callback"
}
func getStatus(t *testing.T, target string) int {
	t.Helper()
	r, e := http.Get(target)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	return r.StatusCode
}
func TestPKCEStateAndPersistence(t *testing.T) {
	for _, kind := range []string{"claude-code", "antigravity"} {
		t.Run(kind, func(t *testing.T) {
			var challenge string
			var exchanges atomic.Int32
			var saved Tokens
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges.Add(1)
				var values map[string]string
				if kind == "claude-code" {
					if r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
						t.Error("missing beta header")
					}
					_ = json.NewDecoder(r.Body).Decode(&values)
				} else {
					r.ParseForm()
					values = map[string]string{}
					for k := range r.Form {
						values[k] = r.Form.Get(k)
					}
				}
				digest := sha256.Sum256([]byte(values["code_verifier"]))
				if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
					t.Error("incorrect PKCE verifier")
				}
				if values["code"] != "approved" {
					t.Error("wrong code")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token":"ACCESS_SECRET","refresh_token":"REFRESH_SECRET","expires_in":3600}`))
			}))
			defer server.Close()
			m := New(nil)
			e := m.endpoints.providers[kind]
			e.token = server.URL
			e.redirect = freeRedirect(t)
			m.endpoints.providers[kind] = e
			m.persist = func(k string, tokens Tokens) error {
				if k != kind {
					t.Error("wrong kind")
				}
				saved = tokens
				return nil
			}
			initial, err := m.Start(kind)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Cancel(initial.ID)
			s := await(t, m, initial.ID, "awaiting_user")
			u, _ := url.Parse(s.URL)
			challenge = u.Query().Get("code_challenge")
			state := u.Query().Get("state")
			if state == "" || challenge == "" {
				t.Fatal("missing PKCE/state")
			}
			if getStatus(t, e.redirect+"?state=bad&code=evil") != 400 {
				t.Fatal("wrong state accepted")
			}
			if exchanges.Load() != 0 {
				t.Fatal("bad state consumed callback")
			}
			if getStatus(t, e.redirect+"?state=bad&error=access_denied") != 400 {
				t.Fatal("wrong error state accepted")
			}
			if getStatus(t, e.redirect+"?state="+state+"&code=approved") != 200 {
				t.Fatal("valid callback rejected")
			}
			s = await(t, m, initial.ID, "done")
			if saved.Access != "ACCESS_SECRET" || saved.Refresh != "REFRESH_SECRET" || saved.ExpiresAt < time.Now().Unix()+3500 {
				t.Fatalf("bad tokens: expiry %d", saved.ExpiresAt)
			}
			b, _ := json.Marshal(s)
			if strings.Contains(string(b), "SECRET") {
				t.Fatal("credential leak")
			}
			if exchanges.Load() != 1 {
				t.Fatal("wrong exchange count")
			}
		})
	}
}
func TestDevicePolling(t *testing.T) {
	var polls atomic.Int32
	var saved atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["client_id"] != defaults().providers["codex"].clientID {
				t.Error("client identity")
			}
			w.Write([]byte(`{"device_auth_id":"device","user_code":"ABCD","interval":1}`))
		case "/poll":
			n := polls.Add(1)
			if n == 1 {
				w.WriteHeader(403)
				return
			}
			if n == 2 {
				w.WriteHeader(404)
				return
			}
			w.Write([]byte(`{"authorization_code":"code","code_verifier":"verifier"}`))
		case "/token":
			r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("redirect_uri") != defaults().providers["codex"].redirect {
				t.Error("exchange form")
			}
			w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":60}`))
		}
	}))
	defer server.Close()
	m := New(nil)
	m.pollUnit = time.Millisecond
	m.endpoints.deviceStart = server.URL + "/start"
	m.endpoints.devicePoll = server.URL + "/poll"
	e := m.endpoints.providers["codex"]
	e.token = server.URL + "/token"
	m.endpoints.providers["codex"] = e
	m.persist = func(string, Tokens) error { saved.Store(true); return nil }
	s, err := m.Start("codex")
	if err != nil {
		t.Fatal(err)
	}
	await(t, m, s.ID, "done")
	if !saved.Load() || polls.Load() != 3 {
		t.Fatal("poll flow incomplete")
	}
}
func TestCancelAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			m := New(nil)
			e := m.endpoints.providers["claude-code"]
			e.redirect = freeRedirect(t)
			m.endpoints.providers["claude-code"] = e
			if timeout {
				m.lifetime = 50 * time.Millisecond
			}
			m.persist = func(string, Tokens) error { t.Error("unexpected persist"); return nil }
			s, err := m.Start("claude-code")
			if err != nil {
				t.Fatal(err)
			}
			await(t, m, s.ID, "awaiting_user")
			if _, err = m.Start("claude-code"); err == nil {
				t.Error("duplicate start allowed")
			}
			if timeout {
				await(t, m, s.ID, "error")
			} else {
				if !m.Cancel(s.ID) || m.Cancel(s.ID) {
					t.Fatal("cancel result")
				}
				await(t, m, s.ID, "cancelled")
			}
			// Wait for asynchronous listener cleanup, then prove the port is reusable.
			u, _ := url.Parse(e.redirect)
			deadline := time.Now().Add(time.Second)
			for {
				l, err := net.Listen("tcp4", u.Host)
				if err == nil {
					l.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("listener leaked")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	m := New(nil)
	if _, err := m.Start("unknown"); err == nil {
		t.Fatal("unknown accepted")
	}
	if _, ok := m.Status("unknown"); ok || m.Cancel("unknown") {
		t.Fatal("unknown status")
	}
}
func TestRefresh(t *testing.T) {
	for _, kind := range []string{"codex", "claude-code", "antigravity"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var v map[string]string
				if kind == "claude-code" {
					json.NewDecoder(r.Body).Decode(&v)
				} else {
					r.ParseForm()
					v = map[string]string{}
					for k := range r.Form {
						v[k] = r.Form.Get(k)
					}
				}
				if v["grant_type"] != "refresh_token" || v["refresh_token"] != "old" {
					t.Error("refresh request")
				}
				w.Write([]byte(`{"access_token":"new","expires_in":100}`))
			}))
			defer server.Close()
			ep := defaults()
			e := ep.providers[kind]
			e.token = server.URL
			ep.providers[kind] = e
			p := &config.Provider{Kind: kind, APIKey: "old-access", RefreshToken: "old", Headers: map[string]string{"project": "keep"}}
			result, err := refresh(context.Background(), p, server.Client(), ep)
			if err != nil {
				t.Fatal(err)
			}
			if result.APIKey != "new" || result.RefreshToken != "old" || !result.OAuth || result.ExpiresAt < time.Now().Unix()+90 {
				t.Fatal("refresh result")
			}
			result.Headers["project"] = "changed"
			if p.APIKey != "old-access" || p.Headers["project"] != "keep" {
				t.Fatal("mutated caller")
			}
		})
	}
}
func TestExchangeFailureSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"SECRET"}`))
	}))
	defer server.Close()
	_, err := exchange(context.Background(), server.Client(), endpoint{token: server.URL}, "codex", url.Values{}, true)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("unsafe/missing error")
	}
}

func TestRefreshRotationAndAccountHeader(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct-123"}}`))
	jwt := "header." + claims + ".signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": jwt, "refresh_token": "rotated", "expires_in": 120})
	}))
	defer server.Close()
	ep := defaults()
	e := ep.providers["codex"]
	e.token = server.URL
	ep.providers["codex"] = e
	p := &config.Provider{Kind: "codex", RefreshToken: "old"}
	got, err := refresh(context.Background(), p, server.Client(), ep)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "rotated" || got.Headers["ChatGPT-Account-Id"] != "acct-123" {
		t.Fatal("rotation/account metadata missing")
	}
	if _, err = refresh(context.Background(), nil, server.Client(), ep); err == nil {
		t.Fatal("nil accepted")
	}
}
func TestValidatedErrorCallback(t *testing.T) {
	m := New(nil)
	e := m.endpoints.providers["claude-code"]
	e.redirect = freeRedirect(t)
	m.endpoints.providers["claude-code"] = e
	m.persist = func(string, Tokens) error { t.Error("persist after denial"); return nil }
	s, err := m.Start("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cancel(s.ID)
	s = await(t, m, s.ID, "awaiting_user")
	u, _ := url.Parse(s.URL)
	if getStatus(t, e.redirect+"?error=access_denied&state="+u.Query().Get("state")) != 200 {
		t.Fatal("valid denial rejected")
	}
	await(t, m, s.ID, "error")
}
