package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
	"golang.org/x/crypto/ssh"
)

func registerVPS(r *Registry) {
	r.Add(&Tool{Name: "vps_run", Category: "ops", Mutates: true, Network: true,
		Description: "Run a command on a saved SSH server (see Servers in the dashboard). Omit command to list servers. Host keys are pinned on first connect (TOFU). Credentials stay encrypted at rest. Default timeout 120s (raise up to 900 for upgrades/restarts).",
		Schema:      Obj(map[string]any{"server": Str("Server name or id."), "command": Str("Shell command (omit to list servers)."), "timeout": Int("Seconds, default 120, max 900.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Server, Command string
				Timeout         int
			}
			Bind(raw, &a)
			if a.Server == "" || a.Command == "" {
				servers, _ := env.Store.ListServers()
				if len(servers) == 0 {
					return Text("no servers saved; add one in the dashboard (Servers)")
				}
				var b strings.Builder
				for _, s := range servers {
					fmt.Fprintf(&b, "%s  %s@%s:%d\n", s.Name, s.User, s.Host, s.Port)
				}
				return Text(b.String())
			}
			srv, err := env.Store.FindServer(a.Server)
			if err != nil {
				return Errorf("no server %q", a.Server)
			}
			if res, blocked := guardCommand(ctx, env, "vps_run", a.Command); blocked {
				return res
			}
			to := a.Timeout
			if to <= 0 {
				to = 120
			}
			if to > 900 {
				to = 900
			}
			client, err := dialSSH(ctx, env, srv)
			if err != nil {
				return Errorf("ssh: %v", err)
			}
			defer client.Close()
			sess, err := client.NewSession()
			if err != nil {
				return Errorf("%v", err)
			}
			defer sess.Close()
			var buf bytes.Buffer
			sess.Stdout, sess.Stderr = &buf, &buf
			cctx, cancel := context.WithTimeout(ctx, time.Duration(to)*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- sess.Run(a.Command) }()
			select {
			case err = <-done:
			case <-cctx.Done():
				sess.Signal(ssh.SIGKILL)
				return Result{Text: Truncate(buf.String(), env.Cfg.Tools.MaxOutputChars) + fmt.Sprintf("\n[timeout after %ds]", to), IsError: true}
			}
			out := buf.String()
			if s, n := secure.Redact(out); n > 0 {
				out = s
			}
			res := Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars), Untrusted: "vps:" + srv.Name}
			if err != nil {
				res.Text += "\n[" + err.Error() + "]"
				res.IsError = true
			}
			return res
		}})

	r.Add(&Tool{Name: "vps_transfer", Category: "ops", Mutates: true, Network: true,
		Description: "Copy a file between the workspace and a saved server over SFTP. direction=upload (local → remote) or download (remote → local). Max 256 MiB.",
		Schema:      Obj(map[string]any{"server": Str("Server name or id."), "direction": Enum("Transfer direction.", "upload", "download"), "local": Str("Workspace-relative path."), "remote": Str("Remote absolute path.")}, "server", "direction", "local", "remote"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Server, Direction, Local, Remote string }
			Bind(raw, &a)
			srv, err := env.Store.FindServer(a.Server)
			if err != nil {
				return Errorf("no server %q", a.Server)
			}
			local, err := env.Resolve(a.Local)
			if err != nil {
				return Errorf("%v", err)
			}
			client, err := dialSSH(ctx, env, srv)
			if err != nil {
				return Errorf("ssh: %v", err)
			}
			defer client.Close()
			msg, err := sftpTransfer(ctx, client, a.Direction, local, a.Remote)
			if err != nil {
				return Errorf("%v", err)
			}
			return Text(msg)
		}})
}

func dialSSH(ctx context.Context, env *Env, srv *store.Server) (*ssh.Client, error) {
	secret := srv.Secret
	if env.Vault != nil && strings.HasPrefix(secret, "v1:") {
		s, err := env.Vault.Open(secret)
		if err != nil {
			return nil, fmt.Errorf("decrypt credential: %v", err)
		}
		secret = s
	}
	var auth ssh.AuthMethod
	if srv.Auth == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("parse key: %v", err)
		}
		auth = ssh.PublicKeys(signer)
	} else {
		auth = ssh.Password(secret)
	}
	cfg := &ssh.ClientConfig{
		User:            srv.User,
		Auth:            []ssh.AuthMethod{auth},
		Timeout:         15 * time.Second,
		HostKeyCallback: pinnedHostKey(env, srv),
	}
	addr := net.JoinHostPort(srv.Host, fmt.Sprint(srv.Port))
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// pinnedHostKey implements trust-on-first-use: the first key seen is stored and
// enforced on later connections.
func pinnedHostKey(env *Env, srv *store.Server) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if srv.HostKey == "" {
			srv.HostKey = fp
			env.Store.SaveServer(srv)
			env.Store.Audit("agent:"+env.SessionID, "vps.hostkey.pinned", srv.Name, fp, "info")
			return nil
		}
		if srv.HostKey != fp {
			return fmt.Errorf("host key mismatch for %s: pinned %s, got %s (possible MITM; update it in Servers if the host legitimately changed)", srv.Name, srv.HostKey, fp)
		}
		return nil
	}
}

const maxTransfer = 256 << 20

// sftpTransfer uses a minimal SFTP-over-SSH implementation via the standard
// "sftp" subsystem through scp-like exec to avoid an extra dependency.
func sftpTransfer(ctx context.Context, client *ssh.Client, dir, local, remote string) (string, error) {
	switch dir {
	case "upload":
		data, err := os.ReadFile(local)
		if err != nil {
			return "", err
		}
		if len(data) > maxTransfer {
			return "", fmt.Errorf("file too large (%d bytes, max 256 MiB)", len(data))
		}
		sess, err := client.NewSession()
		if err != nil {
			return "", err
		}
		defer sess.Close()
		w, _ := sess.StdinPipe()
		base := filepath.Base(remote)
		dirPart := filepath.ToSlash(filepath.Dir(remote))
		go func() {
			fmt.Fprintf(w, "C0644 %d %s\n", len(data), base)
			w.Write(data)
			fmt.Fprint(w, "\x00")
			w.Close()
		}()
		if err := sess.Run("scp -t " + shellQuote(dirPart)); err != nil {
			return "", fmt.Errorf("scp upload failed (is scp installed on the server?): %v", err)
		}
		return fmt.Sprintf("uploaded %s → %s (%d bytes)", local, remote, len(data)), nil
	case "download":
		sess, err := client.NewSession()
		if err != nil {
			return "", err
		}
		defer sess.Close()
		var buf bytes.Buffer
		sess.Stdout = &buf
		sess.Stderr = io.Discard
		if err := sess.Run("cat " + shellQuote(remote)); err != nil {
			return "", fmt.Errorf("download failed: %v", err)
		}
		if buf.Len() > maxTransfer {
			return "", fmt.Errorf("remote file too large")
		}
		tmp := local + ".allones-dl"
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, local); err != nil {
			return "", err
		}
		return fmt.Sprintf("downloaded %s → %s (%d bytes)", remote, local, buf.Len()), nil
	}
	return "", fmt.Errorf("direction must be upload or download")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
