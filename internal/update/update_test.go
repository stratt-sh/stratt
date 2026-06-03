package update

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- install path detection ---

func TestDetectInstallHomebrewPrefixes(t *testing.T) {
	for _, prefix := range homebrewPrefixes {
		if !strings.HasSuffix(prefix, "/") {
			t.Errorf("prefix %q should end with /", prefix)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		path string
		want InstallKind
	}{
		// Formula installs.
		{"/opt/homebrew/Cellar/stratt/0.16.1/bin/stratt", InstallHomebrew},
		{"/usr/local/Cellar/stratt/0.16.1/bin/stratt", InstallHomebrew},
		// Cask installs (stratt's actual shape — the path that regressed).
		{"/opt/homebrew/Caskroom/stratt/0.16.1/stratt", InstallHomebrew},
		{"/usr/local/Caskroom/stratt/0.16.1/stratt", InstallHomebrew},
		// Linuxbrew (both formula and cask live under here).
		{"/home/linuxbrew/.linuxbrew/Cellar/stratt/0.16.1/bin/stratt", InstallHomebrew},
		// Direct installs.
		{"/usr/local/bin/stratt", InstallDirect},
		{"/home/zeb/.local/bin/stratt", InstallDirect},
	}
	for _, tc := range cases {
		if got := classify(tc.path); got != tc.want {
			t.Errorf("classify(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestInstallKindStringer(t *testing.T) {
	cases := map[InstallKind]string{
		InstallDirect:   "direct",
		InstallHomebrew: "homebrew",
		InstallUnknown:  "unknown",
	}
	for k, want := range cases {
		if k.String() != want {
			t.Errorf("%v.String() = %q, want %q", k, k.String(), want)
		}
	}
}

func TestPlatformAssetSuffix(t *testing.T) {
	suffix := PlatformAssetSuffix()
	if !strings.Contains(suffix, "_") {
		t.Errorf("expected `os_arch` form, got %q", suffix)
	}
}

func TestIsCI(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	if IsCI() {
		t.Error("clean env should not be CI")
	}
	t.Setenv("CI", "true")
	if !IsCI() {
		t.Error("$CI=true should be CI")
	}
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	if !IsCI() {
		t.Error("$GITHUB_ACTIONS=true should be CI")
	}
}

// --- IsNewer / semver ---

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.2.4", "1.2.3", true},
		{"v1.2.4", "1.2.3", true},
		{"1.2.4", "v1.2.3", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.2", "1.2.3", false},
		{"2.0.0", "1.99.99", true},
		{"dev", "1.0.0", false}, // unparseable → not newer
		{"1.0.0", "dev", false}, // unparseable current → not newer
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		got := IsNewer(c.candidate, c.current)
		if got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}

// --- LatestRelease against a stub server ---

func TestLatestReleaseStable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v1.5.0",
			Assets: []Asset{
				{Name: "stratt_1.5.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/x"},
			},
		})
	}))
	defer srv.Close()

	c := &http.Client{}
	// Point GitHub API URL at our stub by intercepting via a custom Transport.
	c.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), "https://api.github.com") {
			newURL := srv.URL + strings.TrimPrefix(req.URL.Path, "")
			req2, _ := http.NewRequest(req.Method, newURL, req.Body)
			return http.DefaultTransport.RoundTrip(req2)
		}
		return http.DefaultTransport.RoundTrip(req)
	})
	rel, err := LatestRelease(context.Background(), c, "x/y", ChannelStable)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.5.0" {
		t.Errorf("got %q", rel.TagName)
	}
}

func TestLatestReleasePrereleaseEnumerates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /releases endpoint returns a list.
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "v2.0.0-rc1", Prerelease: true},
			{TagName: "v1.5.0", Prerelease: false},
		})
	}))
	defer srv.Close()

	c := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), "https://api.github.com") {
			req2, _ := http.NewRequest(req.Method, srv.URL+req.URL.Path, req.Body)
			return http.DefaultTransport.RoundTrip(req2)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	rel, err := LatestRelease(context.Background(), c, "x/y", ChannelPrerelease)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v2.0.0-rc1" {
		t.Errorf("prerelease channel should return prerelease first; got %q", rel.TagName)
	}
}

func TestLatestRelease404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	c := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), "https://api.github.com") {
			req2, _ := http.NewRequest(req.Method, srv.URL+req.URL.Path, req.Body)
			return http.DefaultTransport.RoundTrip(req2)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	_, err := LatestRelease(context.Background(), c, "x/y", ChannelStable)
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

// --- PickAsset ---

func TestPickAssetPrefersPlatformSuffix(t *testing.T) {
	r := &Release{
		Assets: []Asset{
			{Name: "stratt_1.0.0_linux_amd64.tar.gz"},
			{Name: "stratt_1.0.0_darwin_arm64.tar.gz"},
			{Name: "stratt_1.0.0_checksums.txt"},
		},
	}
	got := PickAsset(r, "darwin_arm64")
	if got == nil || got.Name != "stratt_1.0.0_darwin_arm64.tar.gz" {
		t.Errorf("got %v", got)
	}
}

func TestPickAssetNoMatchReturnsNil(t *testing.T) {
	r := &Release{Assets: []Asset{{Name: "x.tar.gz"}}}
	if got := PickAsset(r, "darwin_arm64"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestPickAssetIgnoresNonArchives(t *testing.T) {
	r := &Release{Assets: []Asset{
		{Name: "stratt_1.0.0_darwin_arm64.intoto.jsonl"},
		{Name: "stratt_1.0.0_darwin_arm64.tar.gz"},
	}}
	got := PickAsset(r, "darwin_arm64")
	if got == nil || !strings.HasSuffix(got.Name, ".tar.gz") {
		t.Errorf("got %v", got)
	}
}

// --- State persistence ---

func TestStateRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastCheck.IsZero() {
		t.Error("fresh state should have zero LastCheck")
	}

	state.PreviousVersion = "1.0.0"
	state.LatestSeenVersion = "1.1.0"
	state.LastCheck = time.Now()
	if err := SaveState(state); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PreviousVersion != "1.0.0" {
		t.Errorf("PreviousVersion: got %q", loaded.PreviousVersion)
	}
	if loaded.LatestSeenVersion != "1.1.0" {
		t.Errorf("LatestSeenVersion: got %q", loaded.LatestSeenVersion)
	}
}

func TestStateMissingFileReturnsZero(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	state, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.PreviousVersion != "" {
		t.Errorf("expected zero-value, got %+v", state)
	}
}

func TestCacheDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dir, err := CacheDir("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmpHome, ".stratt", "cache", "binaries", "1.2.3")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

// --- Install helpers ---

func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	// echo -n hello | sha256sum
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if digest != want {
		t.Errorf("got %q, want %q", digest, want)
	}
}

// TestSwapInPlace verifies an atomic swap and that the backup is created.
func TestSwapInPlace(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "stratt")
	if err := os.WriteFile(current, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "stratt.new")
	if err := os.WriteFile(newBin, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backups", "1.0.0", "stratt")

	if err := SwapInPlace(current, newBin, backup); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(current)
	if string(got) != "NEW" {
		t.Errorf("current binary not swapped: %q", got)
	}
	bk, _ := os.ReadFile(backup)
	if string(bk) != "OLD" {
		t.Errorf("backup not created: %q", bk)
	}
}

// --- Notifier ---

func TestNotifyIfBehindSilentInCI(t *testing.T) {
	t.Setenv("CI", "true")
	var buf bytes.Buffer
	NotifyIfBehind(&buf, "1.0.0", "myorg/tap/stratt")
	if buf.Len() != 0 {
		t.Errorf("expected silence in CI; got %q", buf.String())
	}
}

func TestNotifyIfBehindSilentForDevBuilds(t *testing.T) {
	t.Setenv("CI", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	state := &State{LatestSeenVersion: "9.9.9"}
	_ = SaveState(state)

	var buf bytes.Buffer
	NotifyIfBehind(&buf, "dev", "myorg/tap/stratt")
	if buf.Len() != 0 {
		t.Errorf("dev build should not be notified; got %q", buf.String())
	}
}

func TestNotifyIfBehindPrintsAdvisoryWhenCacheHasNewer(t *testing.T) {
	t.Setenv("CI", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	_ = SaveState(&State{LatestSeenVersion: "2.0.0"})

	var buf bytes.Buffer
	NotifyIfBehind(&buf, "1.0.0", "myorg/tap/stratt")
	if !strings.Contains(buf.String(), "2.0.0") {
		t.Errorf("expected advisory with 2.0.0; got %q", buf.String())
	}
}

// TestPrintAdvisoryUsesFormulaName — direct check that printAdvisory
// substitutes the fully-qualified formula name for brew installs.
func TestPrintAdvisoryUsesFormulaName(t *testing.T) {
	var buf bytes.Buffer
	printAdvisory(&buf, InstallHomebrew, "1.2.3", "stratt-sh/tap/stratt")
	got := buf.String()
	if !strings.Contains(got, "brew upgrade stratt-sh/tap/stratt") {
		t.Errorf("expected fully-qualified brew command; got %q", got)
	}
}

// TestPrintAdvisoryFallsBackToShortNameForEmptyFormula — defensive: if
// brewFormula isn't set, the advisory still works.
func TestPrintAdvisoryFallsBackToShortNameForEmptyFormula(t *testing.T) {
	var buf bytes.Buffer
	printAdvisory(&buf, InstallHomebrew, "1.2.3", "")
	got := buf.String()
	if !strings.Contains(got, "brew upgrade stratt") {
		t.Errorf("expected fallback to unqualified `stratt`; got %q", got)
	}
}

// TestPrintAdvisoryDirectInstall — non-brew paths suggest `stratt self update`.
func TestPrintAdvisoryDirectInstall(t *testing.T) {
	var buf bytes.Buffer
	printAdvisory(&buf, InstallDirect, "1.2.3", "anything")
	got := buf.String()
	if !strings.Contains(got, "stratt self update") {
		t.Errorf("direct-install advisory should mention `stratt self update`; got %q", got)
	}
	if strings.Contains(got, "brew") {
		t.Errorf("direct-install advisory should not mention brew; got %q", got)
	}
}

func TestNotifyIfBehindSilentWhenUpToDate(t *testing.T) {
	t.Setenv("CI", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	_ = SaveState(&State{LatestSeenVersion: "1.0.0"})

	var buf bytes.Buffer
	NotifyIfBehind(&buf, "1.0.0", "myorg/tap/stratt")
	if buf.Len() != 0 {
		t.Errorf("expected silence when up to date; got %q", buf.String())
	}
}

// TestRefreshNotifierStateSkipsRecentChecks — within 24h of LastCheck,
// refresh is a no-op (R4.12 cadence cap).
func TestRefreshNotifierStateSkipsRecentChecks(t *testing.T) {
	t.Setenv("CI", "")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	before := time.Now().Add(-1 * time.Hour)
	_ = SaveState(&State{LastCheck: before, LatestSeenVersion: "1.0.0"})

	// If the refresh tried to make a network call, this would fail under
	// the assumption that the test env doesn't have a real GitHub repo
	// at the made-up name.  The fact that it returns silently and
	// quickly proves it bailed out of the cadence check.
	RefreshNotifierState(context.Background(), Options{
		Repo:           "x/totally-not-a-real-repo",
		CurrentVersion: "1.0.0",
	})

	state, _ := LoadState()
	if !state.LastCheck.Equal(before) {
		t.Errorf("LastCheck should not be updated within cadence; was %v now %v", before, state.LastCheck)
	}
}

// --- Helpers ---

type roundTrip func(*http.Request) (*http.Response, error)

func (rt roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return rt(req) }

// silence unused-import compile diagnostics during partial builds.
var _ = bytes.NewBuffer
