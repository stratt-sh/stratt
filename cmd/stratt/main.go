// Command stratt is the operations chief for your repo.
//
// stratt is a polyglot task runner that detects your project's stack
// and provides a unified CLI for build, test, release, and deploy.
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
