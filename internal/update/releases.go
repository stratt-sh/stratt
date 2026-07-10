package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// newGitHubRequest builds a GET request with the standard GitHub API
// headers.  Requests authenticate with GH_TOKEN/GITHUB_TOKEN when set
// (the gh CLI convention, GH_TOKEN winning) — without a token, GitHub's
// 60-request-per-hour-per-IP limit applies, which a real user's couple
// of calls never reaches but shared/NAT'd networks (notably GitHub-
// hosted CI runners, where the release smoke test runs) exhaust
// constantly.
func newGitHubRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// githubToken returns the GitHub API token from the environment, using
// the gh CLI's precedence: GH_TOKEN over GITHUB_TOKEN, empty when unset.
func githubToken() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

// apiError converts a non-success GitHub API response into an error,
// turning the common rate-limit case into an actionable message instead
// of dumping the raw JSON body.
func apiError(action string, resp *http.Response) error {
	if isRateLimited(resp) {
		// The limit is per-IP and resets on its own, so for a human the
		// only useful action is to wait.
		when := rateLimitReset(resp)
		if when == "" {
			when = "in a little while"
		}
		return fmt.Errorf("%s: GitHub's API rate limit is exhausted; try again %s", action, when)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("%s: GitHub API returned status %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
}

// isRateLimited reports whether resp is a GitHub rate-limit rejection —
// either the primary limit (403 with X-RateLimit-Remaining: 0) or a
// secondary/abuse limit (429, or a 403 carrying Retry-After).
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return false
	}
	return resp.StatusCode == http.StatusTooManyRequests ||
		resp.Header.Get("X-RateLimit-Remaining") == "0" ||
		resp.Header.Get("Retry-After") != ""
}

// rateLimitReset returns a short human phrase for when the limit resets
// ("in ~42m"), from Retry-After or X-RateLimit-Reset, or "" if unknown.
func rateLimitReset(resp *http.Response) string {
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return "in ~" + (time.Duration(secs) * time.Second).Round(time.Second).String()
		}
	}
	if rl := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")); rl != "" {
		if epoch, err := strconv.ParseInt(rl, 10, 64); err == nil {
			if d := time.Until(time.Unix(epoch, 0)); d > 0 {
				return "in ~" + d.Round(time.Minute).String()
			}
		}
	}
	return ""
}

// Release is the minimal GitHub Releases API representation stratt needs.
type Release struct {
	TagName     string  `json:"tag_name"`
	Prerelease  bool    `json:"prerelease"`
	Draft       bool    `json:"draft"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a Release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
	Size               int64  `json:"size"`
}

// ChannelDefault / ChannelPrerelease control which releases the update
// checker considers (R4.9 / R4.16).  ChannelDefault tracks normal
// (non-prerelease) releases; ChannelPrerelease also surfaces -rc/-beta
// tags.  ("default" replaced the original "stable", which overpromised;
// "stable" is still accepted as a deprecated alias — see NormalizeChannel.)
const (
	ChannelDefault    = "default"
	ChannelPrerelease = "prerelease"
)

// NormalizeChannel resolves a user-supplied channel name to canonical
// form.  Empty means ChannelDefault.  "stable" is accepted as a
// deprecated alias for ChannelDefault (its original name).  Anything
// else is rejected so a typo fails loudly instead of silently meaning
// "default".
func NormalizeChannel(c string) (string, error) {
	switch c {
	case "", ChannelDefault, "stable":
		return ChannelDefault, nil
	case ChannelPrerelease:
		return ChannelPrerelease, nil
	default:
		return "", fmt.Errorf("unknown release channel %q (want %q or %q)", c, ChannelDefault, ChannelPrerelease)
	}
}

// EffectiveChannel resolves which release channel to track, given the
// channel the user explicitly requested (via --channel or [update].channel
// — pass "" when unset) and the running version.
//
// When no channel is explicitly requested AND the running binary is itself
// a prerelease, it defaults to the prerelease channel.  Otherwise an rc
// user is told they're "up to date" while newer rcs exist, because the
// default channel only sees stable releases — which trail the rc line.
// An explicit request (including "default"/"stable") always wins.
func EffectiveChannel(requested, currentVersion string) (string, error) {
	if requested == "" && IsPrereleaseVersion(currentVersion) {
		return ChannelPrerelease, nil
	}
	return NormalizeChannel(requested)
}

// IsPrereleaseVersion reports whether v carries a semver prerelease
// suffix (e.g. "0.19.0-rc.6").  Non-semver inputs ("dev", "") are not
// prereleases.
func IsPrereleaseVersion(v string) bool {
	v = normalizeSemver(v)
	return semver.IsValid(v) && semver.Prerelease(v) != ""
}

// LatestRelease returns the most recent release for repo (owner/name)
// that matches channel.  Returns ErrNoRelease if no eligible release
// exists.  ctx is honored throughout.
//
// HTTP client is injected for testability.  Pass nil for the default
// 10-second-timeout client.
func LatestRelease(ctx context.Context, client *http.Client, repo, channel string) (*Release, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// `/releases/latest` excludes prereleases server-side; only the
	// prerelease channel enumerates.  Everything else (default, empty,
	// or the deprecated "stable" alias) uses /releases/latest.
	if channel == ChannelPrerelease {
		return fetchLatestIncludingPrerelease(ctx, client, repo)
	}
	return fetchLatestDefault(ctx, client, repo)
}

// ErrNoRelease indicates the API returned no eligible release.
var ErrNoRelease = errors.New("no release found")

func fetchLatestDefault(ctx context.Context, client *http.Client, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	rel, err := fetchRelease(ctx, client, url)
	if err != nil {
		return nil, err
	}
	if rel.Prerelease || rel.Draft {
		// /latest should filter these server-side, but guard against API drift.
		return nil, ErrNoRelease
	}
	return rel, nil
}

func fetchLatestIncludingPrerelease(ctx context.Context, client *http.Client, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", repo)
	req, err := newGitHubRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, apiError("checking for releases", resp)
	}
	var list []*Release
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.Draft {
			continue
		}
		return r, nil
	}
	return nil, ErrNoRelease
}

func fetchRelease(ctx context.Context, client *http.Client, url string) (*Release, error) {
	req, err := newGitHubRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNoRelease
	}
	if resp.StatusCode != 200 {
		return nil, apiError("checking for the latest release", resp)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// PickAsset returns the release Asset matching the running platform
// (GoReleaser archive convention: `name_<version>_<os>_<arch>.<ext>`).
// Returns nil if no matching asset is found.
func PickAsset(r *Release, suffix string) *Asset {
	for i := range r.Assets {
		a := &r.Assets[i]
		// Match by suffix-containment to tolerate version-string differences.
		// Skip attestation/checksum sidecars by extension.
		if !strings.Contains(a.Name, suffix) {
			continue
		}
		ext := strings.ToLower(a.Name)
		if strings.HasSuffix(ext, ".tar.gz") || strings.HasSuffix(ext, ".zip") {
			return a
		}
	}
	return nil
}

// IsNewer reports whether candidate is a strictly higher semver than current.
// Returns false on either parse failure (safer default — never downgrade
// to an unparseable version).  R4.8.
func IsNewer(candidate, current string) bool {
	cand := normalizeSemver(candidate)
	cur := normalizeSemver(current)
	if !semver.IsValid(cand) || !semver.IsValid(cur) {
		return false
	}
	return semver.Compare(cand, cur) > 0
}

func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
