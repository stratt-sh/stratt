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
		Version: version,
		Commit:  commit,
		Date:    date,
	}))
}
