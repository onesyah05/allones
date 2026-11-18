package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/llm"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// BrowserPool keeps one Chromium tab per session, sharing a single browser process.
type BrowserPool struct {
	mu        sync.Mutex
	allocCtx  context.Context
	allocStop context.CancelFunc
	browser   context.Context
	tabs      map[string]*tab
	exe       string
	headless  bool
	w, h      int
	profile   string
}

type tab struct {
	ctx    context.Context
	cancel context.CancelFunc
	last   time.Time
}

func NewBrowserPool(exe string, headless bool, w, h int, profileDir string) *BrowserPool {
	return &BrowserPool{tabs: map[string]*tab{}, exe: exe, headless: headless, w: w, h: h, profile: profileDir}
}

func FindChrome(hint string) string {
	if hint != "" {
		return hint
	}
	cands := []string{"chrome", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "msedge", "microsoft-edge"}
	for _, c := range cands {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	var paths []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			base := os.Getenv(env)
			paths = append(paths, filepath.Join(base, `Google\Chrome\Application\chrome.exe`), filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`), filepath.Join(base, `Chromium\Application\chrome.exe`))
		}
	case "darwin":
		paths = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Chromium.app/Contents/MacOS/Chromium", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (bp *BrowserPool) ensure(ctx context.Context) error {
	if bp.browser != nil && bp.browser.Err() == nil {
		return nil
	}
	// A failed launch must not leave a seemingly live context in the pool.
	bp.closeLocked()
	exe := FindChrome(bp.exe)
	if exe == "" {
		return fmt.Errorf("no Chrome/Chromium/Edge found; install one or set tools.browser.executable")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exe),
		chromedp.Flag("headless", bp.headless),
		chromedp.WindowSize(bp.w, bp.h),
		chromedp.UserAgent(browserUA),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	if bp.profile != "" {
		opts = append(opts, chromedp.UserDataDir(bp.profile))
	}
	bp.allocCtx, bp.allocStop = chromedp.NewExecAllocator(context.Background(), opts...)
	var cancel context.CancelFunc
	bp.browser, cancel = chromedp.NewContext(bp.allocCtx)
	if err := startBrowserContext(ctx, bp.browser, cancel); err != nil {
		bp.closeLocked()
		return fmt.Errorf("start browser: %w", err)
	}
	return nil
}

// Keep successful tabs alive across tool calls, but allow Stop and deadlines to
// interrupt initialization too (before an action context can be created).
func startBrowserContext(ctx, target context.Context, cancel context.CancelFunc) error {
	if err := ctx.Err(); err != nil {
		cancel()
		return err
	}
	stop := context.AfterFunc(ctx, cancel)
	err := chromedp.Run(target)
	stop()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (bp *BrowserPool) Tab(request context.Context, session string) (context.Context, error) {
	ctx, stop := context.WithTimeout(request, 30*time.Second)
	defer stop()
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t, ok := bp.tabs[session]; ok && t.ctx.Err() == nil {
		t.last = time.Now()
		return t.ctx, nil
	}
	if err := bp.ensure(ctx); err != nil {
		return nil, err
	}
	tctx, cancel := chromedp.NewContext(bp.browser)
	if err := startBrowserContext(ctx, tctx, cancel); err != nil {
		cancel()
		return nil, err
	}
	bp.tabs[session] = &tab{ctx: tctx, cancel: cancel, last: time.Now()}
	return tctx, nil
}

func (bp *BrowserPool) CloseTab(session string) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if t, ok := bp.tabs[session]; ok {
		t.cancel()
		delete(bp.tabs, session)
	}
}

func (bp *BrowserPool) Close() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.closeLocked()
}

func (bp *BrowserPool) closeLocked() {
	for k, t := range bp.tabs {
		t.cancel()
		delete(bp.tabs, k)
	}
	if bp.allocStop != nil {
		bp.allocStop()
	}
	bp.allocStop = nil
	bp.allocCtx = nil
	bp.browser = nil
}

const snapshotJS = `(() => {
  const sel = 'a[href],button,input,select,textarea,[role=button],[role=link],[role=tab],[role=menuitem],[role=checkbox],[contenteditable=true],summary,[onclick]';
  let i = 0; const out = [];
  document.querySelectorAll('[data-ao]').forEach(e => e.removeAttribute('data-ao'));
  for (const el of document.querySelectorAll(sel)) {
    const r = el.getBoundingClientRect(); const st = getComputedStyle(el);
    if (r.width < 2 || r.height < 2 || st.visibility === 'hidden' || st.display === 'none') continue;
    i++; el.setAttribute('data-ao', String(i));
    let label = (el.innerText || el.value || el.getAttribute('aria-label') || el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('name') || '').trim().replace(/\s+/g,' ').slice(0,80);
    let d = el.tagName.toLowerCase();
    if (el.type) d += '[' + el.type + ']';
    if (el.name) d += ' name=' + el.name;
    if (el.href && el.tagName === 'A') d += ' -> ' + el.href.slice(0,120);
    out.push('[' + i + '] ' + d + (label ? ' "' + label + '"' : ''));
    if (i >= 250) break;
  }
  const text = (document.body ? document.body.innerText : '').replace(/\n{3,}/g,'\n\n').slice(0, 20000);
  return JSON.stringify({url: location.href, title: document.title, elements: out.join('\n'), text});
})()`

func registerBrowser(r *Registry) {
	r.Add(&Tool{Name: "browser", Category: "browser", Mutates: true, Network: true,
		Description: `Drive a real Chromium browser (JavaScript, logins, forms, SPAs). One tab per session.
Actions: navigate(url) · snapshot (page text + numbered interactive elements) · click(ref|selector) · type(ref|selector, text, submit) · press(key) · select(ref|selector, value) · scroll(direction) · back · wait(selector|seconds) · eval(script) · screenshot (image returned to you) · cookies · close.
Typical loop: navigate → snapshot → click/type using the [n] refs → snapshot.`,
		Schema: Obj(map[string]any{
			"action": Enum("Browser action.", "navigate", "snapshot", "click", "type", "press", "select", "scroll", "back", "wait", "eval", "screenshot", "cookies", "close"),
			"url":    Str("URL for navigate."), "ref": Int("Element number from snapshot."), "selector": Str("CSS selector (alternative to ref)."),
			"text": Str("Text to type, key to press, value to select, or direction (up/down)."), "submit": Bool("Press Enter after typing."),
			"script": Str("JavaScript expression for eval."), "seconds": Num("Seconds for wait."), "full_page": Bool("Full-page screenshot."),
		}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Action, URL, Selector, Text, Script string
				Ref                                 int
				Submit                              bool
				Seconds                             float64
				FullPage                            bool `json:"full_page"`
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			bp := env.Services.Browser
			if bp == nil {
				return Errorf("browser is not available")
			}
			if a.Action == "close" {
				bp.CloseTab(env.SessionID)
				return Text("closed the browser tab")
			}
			ctx, actionCancel := context.WithTimeout(ctx, 60*time.Second)
			defer actionCancel()
			tctx, err := bp.Tab(ctx, env.SessionID)
			if err != nil {
				return Errorf("browser: %v", err)
			}
			cctx, cancel := context.WithTimeout(tctx, 60*time.Second)
			defer cancel()
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			sel := a.Selector
			if a.Ref > 0 {
				sel = fmt.Sprintf(`[data-ao="%d"]`, a.Ref)
			}
			snap := func(prefix string) Result {
				var js string
				if err := chromedp.Run(cctx, chromedp.Evaluate(snapshotJS, &js)); err != nil {
					return Errorf("snapshot: %v", err)
				}
				var s struct{ URL, Title, Elements, Text string }
				json.Unmarshal([]byte(js), &s)
				out := fmt.Sprintf("%sURL: %s\nTitle: %s\n\nInteractive elements:\n%s\n\nPage text:\n%s", prefix, s.URL, s.Title, s.Elements, s.Text)
				return Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars), Untrusted: s.URL}
			}
			switch a.Action {
			case "navigate":
				warn, err := env.CheckTarget(ctx, "browser", a.URL)
				if err != nil {
					return Errorf("%v", err)
				}
				if !strings.Contains(a.URL, "://") {
					a.URL = "https://" + a.URL
				}
				if err := chromedp.Run(cctx, chromedp.Navigate(a.URL), chromedp.Sleep(800*time.Millisecond)); err != nil {
					return Errorf("navigate: %v", err)
				}
				p := ""
				if warn != "" {
					p = warn + "\n"
				}
				return snap(p)
			case "snapshot":
				return snap("")
			case "click":
				if sel == "" {
					return Errorf("click needs ref or selector")
				}
				if err := chromedp.Run(cctx, chromedp.ScrollIntoView(sel, chromedp.ByQuery), chromedp.Click(sel, chromedp.ByQuery), chromedp.Sleep(900*time.Millisecond)); err != nil {
					return Errorf("click: %v", err)
				}
				return snap("clicked " + sel + "\n")
			case "type":
				if sel == "" {
					return Errorf("type needs ref or selector")
				}
				acts := []chromedp.Action{chromedp.Clear(sel, chromedp.ByQuery), chromedp.SendKeys(sel, a.Text, chromedp.ByQuery)}
				if a.Submit {
					acts = append(acts, chromedp.SendKeys(sel, kb.Enter, chromedp.ByQuery), chromedp.Sleep(1200*time.Millisecond))
				}
				if err := chromedp.Run(cctx, acts...); err != nil {
					return Errorf("type: %v", err)
				}
				if a.Submit {
					return snap("typed and submitted\n")
				}
				return Text("typed into " + sel)
			case "press":
				key := map[string]string{"enter": kb.Enter, "tab": kb.Tab, "escape": kb.Escape, "esc": kb.Escape, "backspace": kb.Backspace, "arrowdown": kb.ArrowDown, "arrowup": kb.ArrowUp, "pagedown": kb.PageDown, "pageup": kb.PageUp}[strings.ToLower(a.Text)]
				if key == "" {
					key = a.Text
				}
				target := sel
				if target == "" {
					target = "body"
				}
				if err := chromedp.Run(cctx, chromedp.SendKeys(target, key, chromedp.ByQuery), chromedp.Sleep(600*time.Millisecond)); err != nil {
					return Errorf("press: %v", err)
				}
				return Text("pressed " + a.Text)
			case "select":
				if err := chromedp.Run(cctx, chromedp.SetValue(sel, a.Text, chromedp.ByQuery)); err != nil {
					return Errorf("select: %v", err)
				}
				return Text("set " + sel + " = " + a.Text)
			case "scroll":
				dy := 700
				if a.Text == "up" {
					dy = -700
				}
				chromedp.Run(cctx, chromedp.Evaluate(fmt.Sprintf("window.scrollBy(0,%d)", dy), nil), chromedp.Sleep(400*time.Millisecond))
				return snap("")
			case "back":
				if err := chromedp.Run(cctx, chromedp.NavigateBack(), chromedp.Sleep(700*time.Millisecond)); err != nil {
					return Errorf("back: %v", err)
				}
				return snap("")
			case "wait":
				if sel != "" {
					if err := chromedp.Run(cctx, chromedp.WaitVisible(sel, chromedp.ByQuery)); err != nil {
						return Errorf("wait: %v", err)
					}
					return Text(sel + " is visible")
				}
				if a.Seconds <= 0 {
					a.Seconds = 2
				}
				chromedp.Run(cctx, chromedp.Sleep(time.Duration(min(a.Seconds, 30)*float64(time.Second))))
				return Text("waited")
			case "eval":
				var out any
				if err := chromedp.Run(cctx, chromedp.Evaluate(a.Script, &out)); err != nil {
					return Errorf("eval: %v", err)
				}
				b, _ := json.MarshalIndent(out, "", "  ")
				return Result{Text: Truncate(string(b), env.Cfg.Tools.MaxOutputChars), Untrusted: "browser-eval"}
			case "screenshot":
				var png []byte
				var act chromedp.Action = chromedp.CaptureScreenshot(&png)
				if a.FullPage {
					act = chromedp.FullScreenshot(&png, 80)
				}
				if err := chromedp.Run(cctx, act); err != nil {
					return Errorf("screenshot: %v", err)
				}
				dir := filepath.Join(env.Workspace, "screenshots")
				os.MkdirAll(dir, 0o755)
				name := filepath.Join(dir, fmt.Sprintf("shot-%d.png", time.Now().Unix()))
				os.WriteFile(name, png, 0o644)
				mime := "image/png"
				if a.FullPage {
					mime = "image/jpeg"
				}
				rel, _ := filepath.Rel(env.Workspace, name)
				return Result{Text: "screenshot saved to " + filepath.ToSlash(rel), Images: []llm.Image{{MIME: mime, Data: base64.StdEncoding.EncodeToString(png)}}}
			case "cookies":
				var cookies []*network.Cookie
				err := chromedp.Run(cctx, chromedp.ActionFunc(func(c context.Context) error {
					var err error
					cookies, err = network.GetCookies().Do(c)
					return err
				}))
				if err != nil {
					return Errorf("cookies: %v", err)
				}
				var out []string
				for _, c := range cookies {
					v := c.Value
					if len(v) > 12 {
						v = v[:6] + "…"
					}
					out = append(out, fmt.Sprintf("%s=%s domain=%s httpOnly=%v secure=%v sameSite=%s", c.Name, v, c.Domain, c.HTTPOnly, c.Secure, c.SameSite))
				}
				return Text(strings.Join(out, "\n"))
			}
			return Errorf("unknown action %q", a.Action)
		}})
}
