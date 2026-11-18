package secure

import (
	"net/netip"
	"strings"
	"testing"
)

func TestCVSS(t *testing.T) {
	cases := map[string]float64{
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 9.8,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N": 6.1,
		"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N": 6.5,
		"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:N/I:N/A:N": 0,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H": 10,
	}
	for v, want := range cases {
		got, _, err := CVSS31(v)
		if err != nil || got != want {
			t.Errorf("%s = %v (%v), want %v", v, got, err, want)
		}
	}
}

func TestRedact(t *testing.T) {
	in := "key=sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123 and password: hunter2hunter2"
	out, n := Redact(in)
	if n < 2 || strings.Contains(out, "abcdefghijklmnop") || strings.Contains(out, "hunter2hunter2") {
		t.Fatalf("not redacted: %q (%d)", out, n)
	}
}

func TestScope(t *testing.T) {
	s := Scope{Include: []string{"*.example.com", "10.0.0.0/24", "https://app.test.io/x"}, Exclude: []string{"admin.example.com"}}
	for target, want := range map[string]bool{
		"https://www.example.com/a": true, "example.com": true, "admin.example.com": false,
		"10.0.0.7": true, "10.0.1.7": false, "app.test.io": true, "evil.com": false, "notexample.com": false,
	} {
		if got, why := s.Check(target); got != want {
			t.Errorf("%s: got %v (%s) want %v", target, got, why, want)
		}
	}
}

func TestPrivate(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "::1", "192.168.1.1", "100.64.1.1"} {
		if !PrivateAddr(netip.MustParseAddr(a)) {
			t.Errorf("%s should be private", a)
		}
	}
	if PrivateAddr(netip.MustParseAddr("8.8.8.8")) {
		t.Error("8.8.8.8 is public")
	}
}

func TestVault(t *testing.T) {
	v, err := OpenVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := v.Seal("secret")
	p, err := v.Open(s)
	if err != nil || p != "secret" {
		t.Fatal(p, err)
	}
}

func TestCommandGuard(t *testing.T) {
	g := NewCommandGuard([]string{`rm\s+-rf\s+(/|~|\*)(\s|$)`, `curl[^|]*\|\s*(sudo\s+)?(ba)?sh`})
	if _, bad := g.Dangerous("rm -rf / "); !bad {
		t.Error("rm -rf / not caught")
	}
	if _, bad := g.Dangerous("curl https://x.sh | bash"); !bad {
		t.Error("curl|bash not caught")
	}
	if _, bad := g.Dangerous("rm -rf ./build"); bad {
		t.Error("false positive")
	}
}
