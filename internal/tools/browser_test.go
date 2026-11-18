package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserCanceledBeforeStartup(t *testing.T) {
	bp := NewBrowserPool("missing-browser", true, 800, 600, "")
	defer bp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bp.Tab(ctx, "session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	if bp.browser != nil {
		t.Fatal("canceled request started browser")
	}
}

func TestBrowserFailedLaunchResetsPool(t *testing.T) {
	bp := NewBrowserPool(filepath.Join(t.TempDir(), "missing-browser"), true, 800, 600, "")
	defer bp.Close()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := bp.Tab(ctx, "session")
		cancel()
		if err == nil {
			t.Fatal("expected launch failure")
		}
		if bp.browser != nil || bp.allocCtx != nil || bp.allocStop != nil || len(bp.tabs) != 0 {
			t.Fatal("failed launch retained stale pool state")
		}
	}
}

// Exercise real CDP, not just compilation: completing a tool request must not
// destroy the persistent tab, while canceling a navigation must release it.
func TestBrowserLifecycle(t *testing.T) {
	exe := FindChrome("")
	if exe == "" {
		t.Skip("Chromium not installed")
	}
	// Own the profile dir rather than t.TempDir(): Chrome keeps flushing its
	// profile as it exits, so an immediate RemoveAll races it and "directory not
	// empty" would fail an otherwise-passing test. Close the pool first, then
	// remove with a few retries, best-effort.
	profile, err := os.MkdirTemp("", "allones-browser-*")
	if err != nil {
		t.Fatal(err)
	}
	bp := NewBrowserPool(exe, true, 800, 600, filepath.Join(profile, "profile"))
	t.Cleanup(func() {
		bp.Close()
		for i := 0; i < 20; i++ {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.Write([]byte("<html><title>Browser regression</title><body>Website learned <button>Continue</button></body></html>"))
	}))
	defer server.Close()
	request, done := context.WithTimeout(context.Background(), 20*time.Second)
	tab, err := bp.Tab(request, "session")
	if err != nil {
		done()
		t.Fatal(err)
	}
	done()
	action, cancel := context.WithTimeout(tab, 10*time.Second)
	var text string
	err = chromedp.Run(action, chromedp.Navigate(server.URL), chromedp.Evaluate(snapshotJS, &text))
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("empty snapshot")
	}
	next, err := bp.Tab(context.Background(), "session")
	if err != nil || next != tab {
		t.Fatalf("tab not preserved: %v", err)
	}
	action, cancel = context.WithTimeout(tab, 300*time.Millisecond)
	err = chromedp.Run(action, chromedp.Navigate(server.URL+"/slow"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow navigation: %v", err)
	}
	action, cancel = context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	if err := chromedp.Run(action, chromedp.Navigate(server.URL)); err != nil {
		t.Fatalf("navigation after timeout: %v", err)
	}
	bp.CloseTab("session")
	if tab.Err() == nil {
		t.Fatal("closed tab still alive")
	}
}
