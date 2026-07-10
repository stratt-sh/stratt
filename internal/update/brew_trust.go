package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// BrewTrust is the result of checking Homebrew's tap-trust gate
// (Homebrew 6+) for the tap that ships stratt.  Untrusted matters even
// when everything is installed and working: `brew upgrade` SILENTLY
// SKIPS untrusted taps, so a brew-managed stratt quietly stops
// receiving updates.
type BrewTrust int

const (
	// BrewTrustUnknown — couldn't determine (brew missing or version
	// unparsable).  Callers should stay quiet rather than guess.
	BrewTrustUnknown BrewTrust = iota
	// BrewTrustNotRequired — Homebrew < 6: no tap-trust gate.
	BrewTrustNotRequired
	// BrewTrustTrusted — the tap (or the cask itself) is trusted.
	BrewTrustTrusted
	// BrewTrustUntrusted — the gate is active and neither the tap nor
	// the cask is trusted: installs refuse and upgrades silently skip.
	BrewTrustUntrusted
)

// CheckBrewTapTrust reports whether Homebrew's tap-trust gate would
// load casks from tap (or the fully-qualified cask itself).  It runs
// `brew --version` (~150ms) to see whether the gate exists, then reads
// the trust file directly — deliberately NOT `brew list`/`brew info`,
// which refuse to load anything from an untrusted tap and can't
// distinguish "untrusted" from "not installed" cheaply.
func CheckBrewTapTrust(tap, cask string) BrewTrust {
	out, err := exec.Command("brew", "--version").Output()
	if err != nil {
		return BrewTrustUnknown
	}
	major, ok := brewMajorVersion(string(out))
	if !ok {
		return BrewTrustUnknown
	}
	if major < 6 {
		return BrewTrustNotRequired
	}
	data, err := os.ReadFile(brewTrustPath())
	if err != nil {
		// Gate active but nothing has ever been trusted.
		return BrewTrustUntrusted
	}
	return parseBrewTrust(data, tap, cask)
}

// brewTrustPath returns Homebrew's trust registry path:
// $XDG_CONFIG_HOME/homebrew/trust.json when XDG_CONFIG_HOME is set,
// else ~/.homebrew/trust.json (per `brew trust --help`).
func brewTrustPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "homebrew", "trust.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".homebrew", "trust.json")
}

// brewMajorVersion extracts the major version from `brew --version`
// output ("Homebrew 6.0.9\n...").
func brewMajorVersion(out string) (int, bool) {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "Homebrew" && i+1 < len(fields) {
			ver := fields[i+1]
			if dot := strings.IndexByte(ver, '.'); dot > 0 {
				ver = ver[:dot]
			}
			major, err := strconv.Atoi(ver)
			return major, err == nil
		}
	}
	return 0, false
}

// parseBrewTrust scans trust.json for the tap or the fully-qualified
// cask.  The file is a JSON object of string arrays keyed by entry type
// ({"trustedtaps": [...], "trustedcasks": [...], ...}); scanning every
// array keeps this robust to which form of `brew trust` the user ran.
func parseBrewTrust(data []byte, tap, cask string) BrewTrust {
	var registry map[string][]string
	if err := json.Unmarshal(data, &registry); err != nil {
		return BrewTrustUnknown
	}
	for _, entries := range registry {
		for _, e := range entries {
			if e == tap || (cask != "" && e == cask) {
				return BrewTrustTrusted
			}
		}
	}
	return BrewTrustUntrusted
}
