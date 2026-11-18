package tools

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"html"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

func registerCodec(r *Registry) {
	r.Add(&Tool{Name: "codec", Category: "reverse",
		Description: "Encode, decode, hash and identify data. Operations: b64encode/b64decode/b64urldecode, urlencode/urldecode, hexencode/hexdecode, htmlencode/htmldecode, rot13, hash md5/sha1/sha256/sha512, hmac-sha256 (needs key), and identify (guess a hash or encoding by shape).",
		Schema:      Obj(map[string]any{"op": Enum("Operation.", "b64encode", "b64decode", "b64urldecode", "urlencode", "urldecode", "hexencode", "hexdecode", "htmlencode", "htmldecode", "rot13", "md5", "sha1", "sha256", "sha512", "hmac-sha256", "identify"), "input": Str("Input value."), "key": Str("Key for hmac-sha256.")}, "op", "input"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Op, Input, Key string }
			Bind(raw, &a)
			return codec(a.Op, a.Input, a.Key)
		}})

	r.Add(&Tool{Name: "jwt_inspect", Category: "security",
		Description: "Decode a JSON Web Token and assess it: header, claims, algorithm (flagging alg=none, HS/RS confusion risk, injectable kid/jku/x5u headers), expiry and lifetime, and missing standard claims. Decoding only — it does not forge tokens. If a secret is provided it reports whether the HMAC signature verifies.",
		Schema:      Obj(map[string]any{"token": Str("The JWT."), "secret": Str("Optional HMAC secret to check the signature.")}, "token"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Token, Secret string }
			Bind(raw, &a)
			return inspectJWT(a.Token, a.Secret)
		}})
}

func codec(op, in, key string) Result {
	switch op {
	case "b64encode":
		return Text(base64.StdEncoding.EncodeToString([]byte(in)))
	case "b64decode":
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in))
		if err != nil {
			if b, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(in)); err != nil {
				return Errorf("%v", err)
			}
		}
		return Text(string(b))
	case "b64urldecode":
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(in), "="))
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(string(b))
	case "urlencode":
		return Text(url.QueryEscape(in))
	case "urldecode":
		s, err := url.QueryUnescape(in)
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(s)
	case "hexencode":
		return Text(hex.EncodeToString([]byte(in)))
	case "hexdecode":
		b, err := hex.DecodeString(strings.TrimSpace(in))
		if err != nil {
			return Errorf("%v", err)
		}
		return Text(string(b))
	case "htmlencode":
		return Text(html.EscapeString(in))
	case "htmldecode":
		return Text(html.UnescapeString(in))
	case "rot13":
		return Text(strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return 'a' + (r-'a'+13)%26
			case r >= 'A' && r <= 'Z':
				return 'A' + (r-'A'+13)%26
			}
			return r
		}, in))
	case "md5":
		s := md5.Sum([]byte(in))
		return Text(hex.EncodeToString(s[:]))
	case "sha1":
		s := sha1.Sum([]byte(in))
		return Text(hex.EncodeToString(s[:]))
	case "sha256":
		s := sha256.Sum256([]byte(in))
		return Text(hex.EncodeToString(s[:]))
	case "sha512":
		s := sha512.Sum512([]byte(in))
		return Text(hex.EncodeToString(s[:]))
	case "hmac-sha256":
		var h hash.Hash = hmac.New(sha256.New, []byte(key))
		h.Write([]byte(in))
		return Text(hex.EncodeToString(h.Sum(nil)))
	case "identify":
		return Text(identify(strings.TrimSpace(in)))
	}
	return Errorf("unknown op %q", op)
}

func identify(s string) string {
	var guesses []string
	hexOnly := regexp.MustCompile(`^[a-fA-F0-9]+$`).MatchString(s)
	switch {
	case hexOnly && len(s) == 32:
		guesses = append(guesses, "MD5 or NTLM hash")
	case hexOnly && len(s) == 40:
		guesses = append(guesses, "SHA-1 hash")
	case hexOnly && len(s) == 64:
		guesses = append(guesses, "SHA-256 hash")
	case hexOnly && len(s) == 128:
		guesses = append(guesses, "SHA-512 hash")
	}
	if strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$") {
		guesses = append(guesses, "bcrypt hash")
	}
	if strings.HasPrefix(s, "$argon2") {
		guesses = append(guesses, "Argon2 hash")
	}
	if strings.HasPrefix(s, "$6$") {
		guesses = append(guesses, "sha512crypt")
	}
	if strings.Count(s, ".") == 2 && strings.HasPrefix(s, "eyJ") {
		guesses = append(guesses, "JWT (use jwt_inspect)")
	}
	if regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`).MatchString(s) && len(s)%4 == 0 && len(s) >= 8 {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && isMostlyText(b) {
			guesses = append(guesses, "base64 of: "+clip(string(b), 80))
		} else {
			guesses = append(guesses, "base64 (binary)")
		}
	}
	if len(guesses) == 0 {
		return "could not identify; shape does not match a common hash or encoding"
	}
	return strings.Join(guesses, "\n")
}

func inspectJWT(tok, secret string) Result {
	parts := strings.Split(strings.TrimSpace(tok), ".")
	if len(parts) < 2 {
		return Errorf("not a JWT (need at least header.payload)")
	}
	dec := func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) }
	h, err1 := dec(parts[0])
	p, err2 := dec(parts[1])
	if err1 != nil || err2 != nil {
		return Errorf("invalid base64url segments")
	}
	var hdr, claims map[string]any
	json.Unmarshal(h, &hdr)
	json.Unmarshal(p, &claims)
	var b strings.Builder
	hp, _ := json.MarshalIndent(hdr, "", "  ")
	cp, _ := json.MarshalIndent(claims, "", "  ")
	fmt.Fprintf(&b, "Header:\n%s\n\nClaims:\n%s\n\nAssessment:\n", hp, cp)
	alg, _ := hdr["alg"].(string)
	switch {
	case strings.EqualFold(alg, "none"):
		b.WriteString("- alg=none: check whether the server accepts an unsigned token (critical if it does).\n")
	case strings.HasPrefix(strings.ToUpper(alg), "HS"):
		b.WriteString("- HMAC algorithm: signature security rests on the shared secret. If the server also accepts RS*, review for algorithm-confusion.\n")
	case strings.HasPrefix(strings.ToUpper(alg), "RS"), strings.HasPrefix(strings.ToUpper(alg), "ES"), strings.HasPrefix(strings.ToUpper(alg), "PS"):
		b.WriteString("- Asymmetric algorithm: review for HS/RS confusion and for kid/jku/x5u header injection.\n")
	}
	for _, k := range []string{"kid", "jku", "x5u", "jwk"} {
		if v, ok := hdr[k]; ok {
			fmt.Fprintf(&b, "- Header %q=%v present: review for injection/SSRF/path traversal in how the server resolves it.\n", k, v)
		}
	}
	if exp, ok := claims["exp"].(float64); ok {
		t := time.Unix(int64(exp), 0)
		state := "valid"
		if time.Now().After(t) {
			state = "expired"
		}
		fmt.Fprintf(&b, "- exp: %s (%s)", t.Format(time.RFC3339), state)
		if iat, ok := claims["iat"].(float64); ok {
			fmt.Fprintf(&b, "; lifetime %s", (time.Duration(exp-iat) * time.Second).String())
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- No exp claim: the token does not expire.\n")
	}
	for _, k := range []string{"aud", "iss", "sub"} {
		if _, ok := claims[k]; !ok {
			fmt.Fprintf(&b, "- Missing %q claim.\n", k)
		}
	}
	if secret != "" && strings.HasPrefix(strings.ToUpper(alg), "HS") && len(parts) == 3 {
		size := map[byte]func() hash.Hash{'2': sha256.New, '3': sha512.New384, '5': sha512.New}
		nf := size[alg[2]]
		if nf == nil {
			nf = sha256.New
		}
		mac := hmac.New(nf, []byte(secret))
		mac.Write([]byte(parts[0] + "." + parts[1]))
		want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(want), []byte(strings.TrimRight(parts[2], "="))) {
			b.WriteString("- Signature VERIFIES with the supplied secret.\n")
		} else {
			b.WriteString("- Signature does not verify with the supplied secret.\n")
		}
	}
	return Text(b.String())
}

// runCLI executes an allowlisted external CLI directly (no shell) and returns its output.
func runCLI(ctx context.Context, env *Env, label, bin string, args []string, timeoutSec int) Result {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = env.Services.Shells.Get(env.SessionID, env.Workspace)
	cmd.Env = toolEnv(env)
	setProcGroup(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = 3 * time.Second
	var buf syncBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	env.Store.Audit("agent:"+env.SessionID, label+".run", bin, strings.Join(args, " "), "info")
	err := cmd.Run()
	out := buf.String()
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	res := Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars), Untrusted: label}
	if cctx.Err() == context.DeadlineExceeded {
		res.Text += fmt.Sprintf("\n[killed after %ds]", timeoutSec)
		res.IsError = true
	} else if err != nil && (cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0) {
		res.IsError = true
	}
	return res
}
