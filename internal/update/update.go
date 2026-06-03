package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options drives one update / check operation.
type Options struct {
	// Repo is the upstream repository, "owner/name" form.
	Repo string

	// Channel selects which releases are visible to the checker
	// (R4.9 / R4.16).  ChannelStable or ChannelPrerelease.
	Channel string

	// CurrentVersion is the version of the running binary (e.g. "1.2.3"
	// or "dev").  Dev builds are exempt from R4.8 downgrade prevention
	// during explicit Apply, but the notifier still surfaces an
	// "outdated" message for them so contributors are aware.
	CurrentVersion string

	// Stdout / Stderr — required.
	Stdout io.Writer
	Stderr io.Writer

	// HTTPClient — optional; defaults to a 60-second-timeout client.
	HTTPClient *http.Client

	// SkipVerification — set true to bypass attestation verification
	// (only honored for explicit `stratt self update` invocations where
	// the user passed --no-verify).  R4.3 forbids skipping by default.
	SkipVerification bool

	// Policy is the attestation trust anchor.  Defaults to DefaultPolicy
	// (pinned to stratt-sh/stratt).
	Policy AttestationPolicy
}

// Result summarizes what an update operation did.
type Result struct {
	NewVersion       string
	PreviousVersion  string
	BackupPath       string
	AttestationOK    bool
	AttestationError error
	// AlreadyCurrent is true when the running version is already the
	// latest, so nothing was downloaded or swapped.  This is a
	// successful no-op, not an error — callers should report it as
	// "up to date", not fail.
	AlreadyCurrent bool
}

// CheckOnly returns the latest release and whether it's a strict
// upgrade over CurrentVersion.  Does not download or install.
func CheckOnly(ctx context.Context, opts Options) (latest *Release, isNewer bool, err error) {
	if opts.Channel == "" {
		opts.Channel = ChannelStable
	}
	latest, err = LatestRelease(ctx, opts.HTTPClient, opts.Repo, opts.Channel)
	if err != nil {
		return nil, false, err
	}
	return latest, IsNewer(latest.TagName, opts.CurrentVersion), nil
}

// ErrHomebrewManaged is returned when self-update is invoked against a
// brew-installed binary (R4.7).
var ErrHomebrewManaged = errors.New("this binary is managed by Homebrew; use `brew upgrade` (or `stratt self update`, which dispatches to brew for you) instead")

// Apply performs the full self-update flow:
//
//  1. R4.7: refuse if the binary is brew-managed.
//  2. R4.10: refuse if running in CI.
//  3. Fetch latest release for channel.
//  4. R4.8: refuse if not strictly newer than current.
//  5. Download artifact, verify attestation (R4.3).
//  6. Atomic swap (R4.6), preserving the prior binary for rollback (R4.13).
//  7. Update persisted state.
func Apply(ctx context.Context, opts Options) (*Result, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Policy.SubjectRepository == "" {
		opts.Policy = DefaultPolicy
	}
	if opts.Channel == "" {
		opts.Channel = ChannelStable
	}

	kind, exePath := DetectInstall()
	if kind == InstallHomebrew {
		return nil, ErrHomebrewManaged
	}
	if IsCI() {
		return nil, errors.New("self-update disabled in CI environments (R4.10)")
	}

	rel, err := LatestRelease(ctx, opts.HTTPClient, opts.Repo, opts.Channel)
	if err != nil {
		return nil, err
	}
	if !IsNewer(rel.TagName, opts.CurrentVersion) {
		// Already on the latest release: a successful no-op, not an error.
		return &Result{
			AlreadyCurrent:  true,
			NewVersion:      strings.TrimPrefix(rel.TagName, "v"),
			PreviousVersion: opts.CurrentVersion,
		}, nil
	}

	asset := PickAsset(rel, PlatformAssetSuffix())
	if asset == nil {
		return nil, fmt.Errorf("no asset for %s in release %s", PlatformAssetSuffix(), rel.TagName)
	}

	// Working directory for download + verify + extract.
	workDir := filepath.Join(os.TempDir(), "stratt-update-"+fmt.Sprint(time.Now().UnixNano()))
	defer os.RemoveAll(workDir)

	fmt.Fprintf(opts.Stderr, "→ downloading %s\n", asset.Name)
	archivePath, err := DownloadAsset(ctx, opts.HTTPClient, asset, workDir)
	if err != nil {
		return nil, err
	}

	res := &Result{
		NewVersion:      strings.TrimPrefix(rel.TagName, "v"),
		PreviousVersion: opts.CurrentVersion,
	}

	// Attestation verification (R4.3).
	if !opts.SkipVerification {
		fmt.Fprintln(opts.Stderr, "→ verifying attestation")
		digest, err := FileSHA256(archivePath)
		if err != nil {
			return nil, err
		}
		bundleJSON, err := FetchAttestationBundle(ctx, opts.HTTPClient, opts.Repo, digest)
		if err != nil {
			return nil, fmt.Errorf("attestation fetch: %w", err)
		}
		if err := VerifyArtifact(ctx, archivePath, bundleJSON, opts.Policy); err != nil {
			res.AttestationError = err
			return nil, err
		}
		res.AttestationOK = true
	} else {
		fmt.Fprintln(opts.Stderr, "WARNING: skipping attestation verification (--no-verify)")
	}

	// Extract the binary out of the archive.
	binary, err := ExtractBinary(archivePath, workDir, "stratt")
	if err != nil {
		return nil, err
	}

	// Backup the running binary and atomically swap in the new one.
	backupDir, err := CacheDir(opts.CurrentVersion)
	if err != nil {
		return nil, err
	}
	backupPath := filepath.Join(backupDir, "stratt")
	if err := SwapInPlace(exePath, binary, backupPath); err != nil {
		return nil, err
	}
	res.BackupPath = backupPath

	// Persist state.
	state, _ := LoadState()
	state.PreviousVersion = opts.CurrentVersion
	state.LastCheck = time.Now()
	state.LatestSeenVersion = res.NewVersion
	_ = SaveState(state)

	return res, nil
}

// Rollback swaps the running binary back to the previously-installed
// version preserved at ~/.stratt/cache/binaries/<prev>/stratt (R4.13).
func Rollback(_ context.Context) (string, error) {
	kind, exePath := DetectInstall()
	if kind == InstallHomebrew {
		return "", ErrHomebrewManaged
	}
	state, err := LoadState()
	if err != nil {
		return "", err
	}
	if state.PreviousVersion == "" {
		return "", errors.New("no previous version recorded; nothing to roll back to")
	}
	backupDir, err := CacheDir(state.PreviousVersion)
	if err != nil {
		return "", err
	}
	backup := filepath.Join(backupDir, "stratt")
	if _, err := os.Stat(backup); err != nil {
		return "", fmt.Errorf("backup binary missing at %s: %w", backup, err)
	}
	if err := SwapInPlace(exePath, backup, exePath+".stratt-pre-rollback"); err != nil {
		return "", err
	}
	return state.PreviousVersion, nil
}

// VerifyCurrent re-runs the attestation check against the running
// binary (R4.3 paranoid mode).  Useful for security-conscious users
// who installed via direct download and want to verify after the fact.
func VerifyCurrent(ctx context.Context, opts Options) error {
	if opts.Policy.SubjectRepository == "" {
		opts.Policy = DefaultPolicy
	}
	_, exePath := DetectInstall()
	digest, err := FileSHA256(exePath)
	if err != nil {
		return err
	}
	bundleJSON, err := FetchAttestationBundle(ctx, opts.HTTPClient, opts.Repo, digest)
	if err != nil {
		return err
	}
	return VerifyArtifact(ctx, exePath, bundleJSON, opts.Policy)
}

// NotifyIfBehind prints a one-line advisory to w if the cached state
// indicates an update is available.  This runs synchronously and is
// safe to call from PersistentPreRunE — it doesn't make network calls.
//
// brewFormula is the fully-qualified formula name (e.g.
// "stratt-sh/tap/stratt") used in the advisory when stratt was
// installed via Homebrew.  Pass an empty string to fall back to the
// unqualified "stratt".
//
// The cache is refreshed by RefreshNotifierState (typically called in
// a goroutine that the calling process may not wait for) at most once
// per 24h.
//
// No-op in CI and for dev / non-semver versions.  R4.12.
func NotifyIfBehind(w io.Writer, currentVersion, brewFormula string) {
	if IsCI() {
		return
	}
	if currentVersion == "" || currentVersion == "dev" {
		return
	}
	state, err := LoadState()
	if err != nil {
		return
	}
	if state.LatestSeenVersion == "" {
		return
	}
	if !IsNewer(state.LatestSeenVersion, currentVersion) {
		return
	}
	kind, _ := DetectInstall()
	printAdvisory(w, kind, state.LatestSeenVersion, brewFormula)
}

// RefreshNotifierState performs one live release check (capped to once
// per 24h via persisted state) and updates the cache.  Safe to call
// from a goroutine; never writes to user-facing IO.  R4.12.
func RefreshNotifierState(ctx context.Context, opts Options) {
	if IsCI() {
		return
	}
	if opts.CurrentVersion == "" || opts.CurrentVersion == "dev" {
		return
	}
	state, err := LoadState()
	if err != nil {
		return
	}
	if !state.LastCheck.IsZero() && time.Since(state.LastCheck) < 24*time.Hour {
		return
	}
	if opts.Channel == "" {
		opts.Channel = ChannelStable
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	latest, err := LatestRelease(ctx, opts.HTTPClient, opts.Repo, opts.Channel)
	state.LastCheck = time.Now()
	if err == nil {
		state.LatestSeenVersion = strings.TrimPrefix(latest.TagName, "v")
	}
	_ = SaveState(state)
}

// printAdvisory writes the one-line advisory matching the install path.
// brewFormula should be the fully-qualified name (e.g. "owner/tap/name");
// when empty, falls back to the unqualified binary name.
func printAdvisory(w io.Writer, kind InstallKind, latest, brewFormula string) {
	switch kind {
	case InstallHomebrew:
		formula := brewFormula
		if formula == "" {
			formula = "stratt"
		}
		fmt.Fprintf(w, "stratt %s is available — run `brew upgrade %s`\n", latest, formula)
	default:
		fmt.Fprintf(w, "stratt %s is available — run `stratt self update`\n", latest)
	}
}
