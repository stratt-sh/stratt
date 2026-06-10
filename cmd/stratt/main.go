// Command stratt is the operations chief for your repo.
//
// One set of commands for every repo, whatever language it's in: build,
// test, lint, release, and deploy run the same whether the repo is Go,
// Python, Node, or PHP. Stratt detects the toolchain and dispatches,
// replacing the per-repo Makefile.
//
// See README.md for the full pitch.
package main

import (
	"os"
	"runtime/debug"

	"github.com/stratt-sh/stratt/internal/cli"
)

// These are injected at link time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Run(cli.BuildInfo{
		Version: resolveVersion(),
		Commit:  commit,
		Date:    date,
	}))
}

// resolveVersion returns the GoReleaser-stamped version, or — for builds
// produced by `go install ...@vX.Y.Z` (which don't run ldflags) — the
// module version recorded in the build info.  Without this, such installs
// report "dev", which silently disables the update notifier and confuses
// required_stratt checks.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}
