// Package update implements `stratt self update` and friends:
// self-update with GitHub Artifact Attestation verification, atomic
// swap, rollback, and the every-invocation update notifier (R4).
package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallKind categorizes how the current stratt binary was installed.
// Homebrew installs MUST NOT be self-updated (R4.7).
type InstallKind int

const (
	InstallDirect   InstallKind = iota // ~/.local/bin/, /usr/local/bin/, etc.
	InstallHomebrew                    // brew Cellar/ (formula) or Caskroom/ (cask)
	InstallUnknown                     // couldn't determine
)

func (k InstallKind) String() string {
	switch k {
	case InstallDirect:
		return "direct"
	case InstallHomebrew:
		return "homebrew"
	default:
		return "unknown"
	}
}

// homebrewPrefixes are path prefixes that indicate brew ownership.
// EvalSymlinks should be applied to the exe path before testing because
// brew ships symlinks under .../bin/ that point into Cellar/ (formulae)
// or Caskroom/ (casks).  stratt ships as a cask, so the Caskroom paths
// matter — without them a cask install looks like a direct install and
// `stratt self update` would try to overwrite a brew-managed binary
// (forbidden by R4.7).
var homebrewPrefixes = []string{
	"/opt/homebrew/Cellar/",       // macOS arm64, formula
	"/opt/homebrew/Caskroom/",     // macOS arm64, cask
	"/usr/local/Cellar/",          // macOS amd64 (intel), formula
	"/usr/local/Caskroom/",        // macOS amd64 (intel), cask
	"/home/linuxbrew/.linuxbrew/", // linuxbrew (covers both Cellar and Caskroom)
}

// DetectInstall classifies the install method of the running binary.
// Returns InstallUnknown on any error so callers can decide whether
// to be conservative (refuse update) or permissive (proceed).
func DetectInstall() (InstallKind, string) {
	exe, err := os.Executable()
	if err != nil {
		return InstallUnknown, ""
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Fall back to the unresolved path; brew's bin/ symlinks resolve,
		// but if that fails we'd rather not block self-update on a
		// transient filesystem issue.
		resolved = exe
	}
	return classify(resolved), resolved
}

// classify maps a resolved executable path to an InstallKind by testing
// it against the known Homebrew prefixes.  Split out from DetectInstall
// so the path-matching logic is unit-testable without relocating the
// running binary.
func classify(resolved string) InstallKind {
	for _, prefix := range homebrewPrefixes {
		if strings.HasPrefix(resolved, prefix) {
			return InstallHomebrew
		}
	}
	return InstallDirect
}

// IsCI reports whether self-update should be skipped due to running
// in an automated environment.  Honors $CI (de facto) and
// $GITHUB_ACTIONS (explicit).  R4.10.
func IsCI() bool {
	return os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != ""
}

// PlatformAssetSuffix returns the GoReleaser-conventional archive suffix
// for the running platform, e.g. "darwin_arm64" or "linux_amd64".  Used
// to pick the correct asset off a GitHub Release.
func PlatformAssetSuffix() string {
	return runtime.GOOS + "_" + runtime.GOARCH
}
