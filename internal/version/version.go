// Package version carries the build version and the upstream repository, so the
// server, the dashboard and the self-updater all agree on one number.
package version

// Version is the running build. It defaults to the source tree's VERSION and is
// overridden at release time with:
//
//	-ldflags "-X github.com/allones/allones/internal/version.Version=1.2.3"
var Version = "1.0.0"

// Repo is the GitHub "owner/name" the self-updater checks for new releases.
const Repo = "onesyah05/allones"

// Current returns the running version without a leading "v".
func Current() string { return normalize(Version) }

func normalize(v string) string {
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		return v[1:]
	}
	return v
}
