package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type AuditHit struct {
	File, Rule, CWE, Code string
	Line                  int
}

// auditRules are leads for manual review, matched line by line.
var auditRules = []struct {
	rule, cwe, exts string
	re              *regexp.Regexp
}{
	{"sql-string-concat", "CWE-89", ".go.py.js.ts.php.java.cs.rb", regexp.MustCompile("(?i)(execute|query|exec|raw|prepare)\\s*\\(\\s*[\"'`][^\"'`]*(select|insert|update|delete|where)[^\"'`]*[\"'`]\\s*(\\+|%|\\.\\s*format|\\$\\{|f[\"'])")},
	{"sql-fstring", "CWE-89", ".py", regexp.MustCompile(`(?i)\.execute\(\s*f["'].*(select|insert|update|delete)`)},
	{"os-command", "CWE-78", ".py", regexp.MustCompile(`subprocess\.\w+\([^)]*shell\s*=\s*True|os\.system\(|os\.popen\(`)},
	{"os-command", "CWE-78", ".js.ts", regexp.MustCompile(`child_process|\bexecSync\(|\bexec\(\s*[^)]*(\+|\$\{)`)},
	{"os-command", "CWE-78", ".php", regexp.MustCompile(`\b(system|shell_exec|passthru|exec|popen|proc_open)\s*\(\s*\$`)},
	{"code-eval", "CWE-95", ".js.ts.py.php.rb", regexp.MustCompile(`\beval\s*\(|new Function\s*\(`)},
	{"xss-sink", "CWE-79", ".js.ts.jsx.tsx.vue.html", regexp.MustCompile(`\.innerHTML\s*=|dangerouslySetInnerHTML|document\.write\(|v-html=|\.outerHTML\s*=|insertAdjacentHTML`)},
	{"unsafe-deserialization", "CWE-502", ".py", regexp.MustCompile(`pickle\.loads?\(|yaml\.load\([^)]*\)|marshal\.loads\(`)},
	{"unsafe-deserialization", "CWE-502", ".java", regexp.MustCompile(`ObjectInputStream|XMLDecoder|\.readObject\(`)},
	{"unsafe-deserialization", "CWE-502", ".php", regexp.MustCompile(`\bunserialize\s*\(\s*\$`)},
	{"unsafe-deserialization", "CWE-502", ".cs", regexp.MustCompile(`BinaryFormatter|TypeNameHandling\.(All|Auto)`)},
	{"tls-verify-disabled", "CWE-295", ".go.py.js.ts.java.cs", regexp.MustCompile(`InsecureSkipVerify:\s*true|verify\s*=\s*False|rejectUnauthorized:\s*false|NODE_TLS_REJECT_UNAUTHORIZED|TrustAllCerts`)},
	{"weak-hash", "CWE-327", ".go.py.js.ts.java.php.cs.rb", regexp.MustCompile(`(?i)crypto/md5|crypto/sha1|hashlib\.(md5|sha1)|createHash\(\s*["'](md5|sha1)`)},
	{"weak-random", "CWE-338", ".js.ts.py.go.java", regexp.MustCompile(`Math\.random\(\)|random\.random\(\)|"math/rand"`)},
	{"path-traversal", "CWE-22", ".js.ts.py.php.go.java", regexp.MustCompile(`(readFile|sendFile|open|file_get_contents|include|require)\s*\(\s*[^)]*(req\.(query|params|body)|\$_(GET|POST|REQUEST)|request\.(args|form))`)},
	{"ssrf-sink", "CWE-918", ".js.ts.py.php.go", regexp.MustCompile(`(fetch|axios\.\w+|requests\.(get|post)|http\.Get|file_get_contents)\s*\(\s*[^)]*(req\.(query|params|body)|\$_(GET|POST|REQUEST)|request\.(args|form))`)},
	{"open-redirect", "CWE-601", ".js.ts.py.php", regexp.MustCompile(`(res\.redirect|redirect)\s*\(\s*(req\.(query|params|body)|request\.args)`)},
	{"cors-permissive", "CWE-942", ".js.ts.py.go.java", regexp.MustCompile(`(?i)Access-Control-Allow-Origin["']?\s*[,:]\s*["']\*|origin:\s*(true|["']\*["'])`)},
	{"hardcoded-credential", "CWE-798", ".go.py.js.ts.java.php.cs.rb.yaml.yml.env.json", regexp.MustCompile(`(?i)(password|passwd|secret|api_?key|token)\s*[:=]\s*["'][^"'\s$<{]{8,}["']`)},
	{"jwt-no-verify", "CWE-347", ".js.ts.py.go", regexp.MustCompile(`(?i)algorithms?\s*[:=]\s*\[?["']none|verify_signature["']?\s*:\s*False|verify\s*=\s*False`)},
	{"debug-enabled", "CWE-489", ".py.js.ts.env", regexp.MustCompile(`(?i)DEBUG\s*=\s*True|\.run\([^)]*debug\s*=\s*True`)},
	{"xxe-parser", "CWE-611", ".java.cs.php.py", regexp.MustCompile(`DocumentBuilderFactory|XMLInputFactory|resolve_entities\s*=\s*True|XmlResolver\s*=`)},
}

func CodeAudit(root string, max int) []AuditHit {
	var hits []AuditHit
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == "" {
			return nil
		}
		info, _ := d.Info()
		if info == nil || info.Size() > 2<<20 || strings.Contains(p, ".min.") {
			return nil
		}
		var rules []int
		for i, r := range auditRules {
			if strings.Contains(r.exts+".", ext+".") {
				rules = append(rules, i)
			}
		}
		if len(rules) == 0 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for ln, line := range strings.Split(string(b), "\n") {
			if len(line) > 1000 {
				continue
			}
			for _, i := range rules {
				if auditRules[i].re.MatchString(line) {
					hits = append(hits, AuditHit{File: p, Line: ln + 1, Rule: auditRules[i].rule, CWE: auditRules[i].cwe, Code: line})
					break
				}
			}
			if len(hits) >= max {
				return fs.SkipAll
			}
		}
		return nil
	})
	return hits
}
