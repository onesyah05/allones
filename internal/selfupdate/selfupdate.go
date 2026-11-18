// Package selfupdate checks GitHub for a newer release and, on request, replaces
// the running binary with it in place. It only ever talks to GitHub's API and
// release downloads for the project's own repository.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/version"
)

// Release is the subset of a GitHub release the updater needs.
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Notes       string    `json:"body"`
	URL         string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Status is what the dashboard shows.
type Status struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"update_available"`
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
	AssetURL    string `json:"-"`
}

var (
	mu       sync.Mutex
	cached   *Status
	cachedAt time.Time
)

const cacheTTL = 15 * time.Minute

var client = &http.Client{Timeout: 20 * time.Second}

// Check returns the update status, cached for a short while so the dashboard can
// poll freely. A network error returns a "no update" status, never an error that
// would scare the UI — being offline is not a failure.
func Check(ctx context.Context) *Status {
	mu.Lock()
	if cached != nil && time.Since(cachedAt) < cacheTTL {
		c := *cached
		mu.Unlock()
		return &c
	}
	mu.Unlock()

	st := &Status{Current: version.Current()}
	rel, err := latest(ctx)
	if err == nil && rel != nil {
		st.Latest = normalize(rel.Tag)
		st.Notes = rel.Notes
		st.URL = rel.URL
		if !rel.PublishedAt.IsZero() {
			st.PublishedAt = rel.PublishedAt.Format(time.RFC3339)
		}
		st.AssetURL = assetFor(rel)
		st.Available = st.AssetURL != "" && compare(st.Latest, st.Current) > 0
	}

	mu.Lock()
	cached = st
	cachedAt = time.Now()
	mu.Unlock()
	c := *st
	return &c
}

// latest fetches the newest published (non-draft, non-prerelease) release.
func latest(ctx context.Context) (*Release, error) {
	url := "https://api.github.com/repos/" + version.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "allones-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no releases yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease {
		return nil, nil
	}
	return &rel, nil
}

// assetFor returns the download URL of the release asset for this OS and arch.
// Assets are named allones_<goos>_<goarch>[.exe] by the release workflow.
func assetFor(rel *Release) string {
	want := "allones_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, want) {
			return a.URL
		}
	}
	return ""
}

// Apply downloads the matching asset and swaps it in for the running binary.
// It returns once the new file is in place; the caller restarts the process.
func Apply(ctx context.Context) error {
	st := Check(ctx)
	if !st.Available || st.AssetURL == "" {
		return fmt.Errorf("no update available for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)

	// Download beside the current binary so the final rename stays on one volume.
	tmp, err := os.CreateTemp(dir, ".allones-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // harmless once renamed away

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, st.AssetURL, nil)
	if err != nil {
		tmp.Close()
		return err
	}
	req.Header.Set("User-Agent", "allones-selfupdate")
	resp, err := client.Do(req)
	if err != nil {
		tmp.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 512<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	// Rename the running binary aside (allowed on Windows), then move the new one
	// into its place. Keep the old copy so a failed start can be rolled back by hand.
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("move current binary: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		// Put the original back if the swap failed.
		_ = os.Rename(old, exe)
		return fmt.Errorf("install new binary: %w", err)
	}

	// The next check must not report the now-stale cache.
	mu.Lock()
	cached = nil
	mu.Unlock()
	return nil
}

// Restart launches the freshly installed binary with the same arguments and
// environment, then exits the current process so the new one takes over.
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	// Give the child a moment to bind before the parent releases the port.
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

func normalize(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		return v[1:]
	}
	return v
}

// compare returns >0 if a is newer than b, using dotted numeric semantics and
// ignoring any pre-release suffix.
func compare(a, b string) int {
	as := parts(a)
	bs := parts(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parts(v string) []int {
	v = normalize(v)
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	segs := strings.Split(v, ".")
	out := make([]int, 0, len(segs))
	for _, s := range segs {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		out = append(out, n)
	}
	return out
}
