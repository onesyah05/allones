package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/secure"
)

func registerRecon(r *Registry) {
	r.Add(&Tool{Name: "dns_lookup", Category: "recon", Network: true,
		Description: "Resolve DNS records for a host: A, AAAA, CNAME, MX, NS, TXT, and reverse PTR. Pure-Go, no external tools.",
		Schema:      Obj(map[string]any{"host": Str("Hostname or IP."), "types": StrArr("Record types (default A,AAAA,CNAME,MX,NS,TXT).")}, "host"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Host  string
				Types []string
			}
			Bind(raw, &a)
			host := secure.HostOf(a.Host)
			if host == "" {
				return Errorf("need a host")
			}
			if len(a.Types) == 0 {
				a.Types = []string{"A", "AAAA", "CNAME", "MX", "NS", "TXT"}
			}
			res := &net.Resolver{}
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			var b strings.Builder
			if ip := net.ParseIP(host); ip != nil {
				names, _ := res.LookupAddr(cctx, host)
				fmt.Fprintf(&b, "PTR: %s\n", strings.Join(names, ", "))
				return Text(b.String())
			}
			for _, t := range a.Types {
				switch strings.ToUpper(t) {
				case "A", "AAAA":
					ips, _ := res.LookupIP(cctx, map[string]string{"A": "ip4", "AAAA": "ip6"}[strings.ToUpper(t)], host)
					for _, ip := range ips {
						fmt.Fprintf(&b, "%s: %s\n", strings.ToUpper(t), ip)
					}
				case "CNAME":
					if c, err := res.LookupCNAME(cctx, host); err == nil && c != host+"." {
						fmt.Fprintf(&b, "CNAME: %s\n", c)
					}
				case "MX":
					mx, _ := res.LookupMX(cctx, host)
					for _, m := range mx {
						fmt.Fprintf(&b, "MX: %d %s\n", m.Pref, m.Host)
					}
				case "NS":
					ns, _ := res.LookupNS(cctx, host)
					for _, n := range ns {
						fmt.Fprintf(&b, "NS: %s\n", n.Host)
					}
				case "TXT":
					txt, _ := res.LookupTXT(cctx, host)
					for _, x := range txt {
						fmt.Fprintf(&b, "TXT: %s\n", clip(x, 300))
					}
				}
			}
			if b.Len() == 0 {
				return Text("no records")
			}
			return External("dns", b.String())
		}})

	r.Add(&Tool{Name: "port_scan", Category: "recon", Mutates: true, Network: true,
		Description: "TCP connect scan of a host (pure-Go, no root). Pass ports as a list, ranges ('1-1024'), or 'top' for ~100 common ports. Reports open ports with a quick banner. Scope-checked. For large/UDP/stealth scans use sec_tool nmap.",
		Schema: Obj(map[string]any{"host": Str("Target host or IP."), "ports": Str("e.g. '80,443,8080', '1-1024', or 'top'."),
			"timeout_ms": Int("Per-port connect timeout, default 800."), "concurrency": Int("Parallel connections, default 200.")}, "host"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Host, Ports            string
				TimeoutMs, Concurrency int
			}
			Bind(raw, &a)
			host := secure.HostOf(a.Host)
			if _, err := env.CheckTarget(ctx, "port_scan", host); err != nil {
				return Errorf("%v", err)
			}
			ports := parsePorts(a.Ports)
			if len(ports) == 0 {
				return Errorf("no ports; give a list, range, or 'top'")
			}
			if len(ports) > 5000 {
				return Errorf("too many ports (%d); narrow the range or use sec_tool nmap", len(ports))
			}
			to := time.Duration(a.TimeoutMs) * time.Millisecond
			if to <= 0 {
				to = 800 * time.Millisecond
			}
			conc := a.Concurrency
			if conc <= 0 || conc > 500 {
				conc = 200
			}
			type openPort struct {
				port   int
				banner string
			}
			var mu sync.Mutex
			var open []openPort
			sem := make(chan struct{}, conc)
			var wg sync.WaitGroup
			cctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
			defer cancel()
			for _, p := range ports {
				if cctx.Err() != nil {
					break
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(port int) {
					defer wg.Done()
					defer func() { <-sem }()
					d := net.Dialer{Timeout: to}
					conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
					if err != nil {
						return
					}
					banner := grabBanner(conn, port)
					conn.Close()
					mu.Lock()
					open = append(open, openPort{port, banner})
					mu.Unlock()
				}(p)
			}
			wg.Wait()
			sort.Slice(open, func(i, j int) bool { return open[i].port < open[j].port })
			if len(open) == 0 {
				return Text(fmt.Sprintf("%s: no open ports among %d scanned", host, len(ports)))
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s — %d open of %d scanned:\n", host, len(open), len(ports))
			for _, o := range open {
				svc := commonServices[o.port]
				fmt.Fprintf(&b, "%d/tcp open %s %s\n", o.port, svc, o.banner)
			}
			return External("port_scan", b.String())
		}})

	r.Add(&Tool{Name: "http_probe", Category: "recon", Network: true,
		Description: "Probe one or more URLs/hosts for liveness and fingerprint: status, title, server, redirect, security headers, and detected technologies. Like httpx but built in.",
		Schema:      Obj(map[string]any{"targets": StrArr("URLs or hosts (http/https tried if no scheme)."), "target": Str("Single target (alternative).")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Targets []string
				Target  string
			}
			Bind(raw, &a)
			if a.Target != "" {
				a.Targets = append(a.Targets, a.Target)
			}
			if len(a.Targets) == 0 {
				return Errorf("need targets")
			}
			var b strings.Builder
			for _, t := range a.Targets {
				if _, err := env.CheckTarget(ctx, "http_probe", t); err != nil {
					fmt.Fprintf(&b, "%s: %v\n\n", t, err)
					continue
				}
				b.WriteString(probeOne(ctx, env, t))
				b.WriteString("\n")
			}
			return External("http_probe", Truncate(b.String(), env.Cfg.Tools.MaxOutputChars))
		}})
}

func probeOne(ctx context.Context, env *Env, target string) string {
	schemes := []string{""}
	if !strings.Contains(target, "://") {
		schemes = []string{"https://", "http://"}
	}
	var b strings.Builder
	for _, s := range schemes {
		url := s + target
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", browserUA)
		resp, err := HTTPClient(env, 20*time.Second, false, true).Do(req)
		if err != nil {
			if s == "http://" || s == "" {
				fmt.Fprintf(&b, "%s: unreachable (%v)\n", url, err)
			}
			continue
		}
		body := make([]byte, 8000)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		title := extractTitle(string(body[:n]))
		fmt.Fprintf(&b, "%s [%d] %s\n", url, resp.StatusCode, title)
		if loc := resp.Header.Get("Location"); loc != "" {
			fmt.Fprintf(&b, "  -> redirect: %s\n", loc)
		}
		if srv := resp.Header.Get("Server"); srv != "" {
			fmt.Fprintf(&b, "  server: %s\n", srv)
		}
		if pb := resp.Header.Get("X-Powered-By"); pb != "" {
			fmt.Fprintf(&b, "  x-powered-by: %s\n", pb)
		}
		if techs := detectTech(resp.Header, string(body[:n])); len(techs) > 0 {
			fmt.Fprintf(&b, "  tech: %s\n", strings.Join(techs, ", "))
		}
		var missing []string
		for _, h := range []string{"Strict-Transport-Security", "Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
			if resp.Header.Get(h) == "" {
				missing = append(missing, h)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(&b, "  missing security headers: %s\n", strings.Join(missing, ", "))
		}
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			c := resp.TLS.PeerCertificates[0]
			fmt.Fprintf(&b, "  tls: %s, CN=%s, expires %s", tlsVersion(resp.TLS.Version), c.Subject.CommonName, c.NotAfter.Format("2006-01-02"))
			if time.Now().After(c.NotAfter) {
				b.WriteString(" [EXPIRED]")
			}
			if len(c.DNSNames) > 0 {
				fmt.Fprintf(&b, " SAN=%s", clip(strings.Join(c.DNSNames, ","), 200))
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	return b.String()
}

func grabBanner(conn net.Conn, port int) string {
	conn.SetDeadline(time.Now().Add(1200 * time.Millisecond))
	// For HTTP-ish ports, send a minimal request to elicit a response line.
	if port == 80 || port == 8080 || port == 8000 || port == 8888 {
		conn.Write([]byte("HEAD / HTTP/1.0\r\n\r\n"))
	}
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	s := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' || r == '\n' {
			return ' '
		}
		return r
	}, string(buf[:n])))
	return clip(s, 120)
}

func parsePorts(spec string) []int {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "top") {
		var out []int
		for p := range commonServices {
			out = append(out, p)
		}
		sort.Ints(out)
		return out
	}
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, _ := strconv.Atoi(strings.TrimSpace(lo))
			b, _ := strconv.Atoi(strings.TrimSpace(hi))
			for p := a; p <= b && p <= 65535; p++ {
				if p > 0 && !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		} else if p, err := strconv.Atoi(part); err == nil && p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

var commonServices = map[int]string{
	21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "dns", 80: "http", 110: "pop3", 111: "rpcbind",
	135: "msrpc", 139: "netbios", 143: "imap", 161: "snmp", 389: "ldap", 443: "https", 445: "smb",
	465: "smtps", 587: "submission", 636: "ldaps", 993: "imaps", 995: "pop3s", 1025: "nfs", 1433: "mssql",
	1521: "oracle", 1723: "pptp", 2049: "nfs", 2082: "cpanel", 2083: "cpanel", 2375: "docker", 2376: "docker",
	2379: "etcd", 3000: "node", 3128: "proxy", 3306: "mysql", 3389: "rdp", 4444: "metasploit", 5000: "http-alt",
	5432: "postgres", 5601: "kibana", 5672: "amqp", 5900: "vnc", 5984: "couchdb", 6379: "redis", 6443: "k8s-api",
	7001: "weblogic", 8000: "http-alt", 8008: "http-alt", 8080: "http-proxy", 8081: "http-alt", 8086: "influxdb",
	8088: "http-alt", 8443: "https-alt", 8888: "http-alt", 9000: "php-fpm", 9043: "websphere", 9090: "prometheus",
	9200: "elasticsearch", 9300: "elasticsearch", 10000: "webmin", 11211: "memcached", 15672: "rabbitmq",
	27017: "mongodb", 27018: "mongodb", 50070: "hadoop",
}

func detectTech(h http.Header, body string) []string {
	var t []string
	add := func(s string) {
		for _, x := range t {
			if x == s {
				return
			}
		}
		t = append(t, s)
	}
	lb := strings.ToLower(body)
	for needle, name := range map[string]string{
		"wp-content": "WordPress", "/_next/": "Next.js", "__nuxt": "Nuxt", "ng-version": "Angular",
		"react": "React", "vue.js": "Vue", "laravel_session": "Laravel", "csrfmiddlewaretoken": "Django",
		"x-drupal": "Drupal", "joomla": "Joomla", "shopify": "Shopify", "cdn.jsdelivr": "jsDelivr",
		"gstatic.com/recaptcha": "reCAPTCHA", "cloudflare": "Cloudflare",
	} {
		if strings.Contains(lb, needle) {
			add(name)
		}
	}
	for k := range h {
		lk := strings.ToLower(k)
		switch {
		case lk == "x-aspnet-version" || lk == "x-aspnetmvc-version":
			add("ASP.NET")
		case lk == "x-drupal-cache":
			add("Drupal")
		case strings.HasPrefix(lk, "cf-"):
			add("Cloudflare")
		case lk == "x-shopify-stage":
			add("Shopify")
		}
	}
	for _, c := range h["Set-Cookie"] {
		lc := strings.ToLower(c)
		for needle, name := range map[string]string{"laravel_session": "Laravel", "phpsessid": "PHP", "jsessionid": "Java", "express": "Express", "connect.sid": "Express", "ci_session": "CodeIgniter"} {
			if strings.Contains(lc, needle) {
				add(name)
			}
		}
	}
	return t
}

func extractTitle(body string) string {
	lb := strings.ToLower(body)
	i := strings.Index(lb, "<title")
	if i < 0 {
		return ""
	}
	j := strings.Index(lb[i:], ">")
	if j < 0 {
		return ""
	}
	start := i + j + 1
	end := strings.Index(lb[start:], "</title>")
	if end < 0 {
		return ""
	}
	return clip(strings.Join(strings.Fields(body[start:start+end]), " "), 120)
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	}
	return "TLS?"
}
