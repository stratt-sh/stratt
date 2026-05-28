package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/detect"
)

// newDocsCmd implements `stratt docs build` and `stratt docs serve`,
// dispatching to mkdocs or sphinx based on detection (R3 "docs" chain).
func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Build, serve, or clean project documentation",
		Long:  `Subcommands dispatch to the detected docs toolchain (MkDocs, Sphinx, or Hugo).`,
	}
	cmd.AddCommand(newDocsActionCmd("build"))
	cmd.AddCommand(newDocsActionCmd("serve"))
	cmd.AddCommand(newDocsCleanCmd())
	return cmd
}

// newDocsCleanCmd implements `stratt docs clean` — removes just the
// docs build artifacts (not the rest of `stratt clean`'s targets).
// Useful when you want a clean docs rebuild without touching the
// venv / caches.
func newDocsCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove the documentation build artifacts",
		Long: `Removes only the docs output directories for the detected toolchain.

  mkdocs    → site/
  sphinx    → docs/_build/, docs/_autosummary/
  hugo      → <hugo source>/public/

Useful when you want to force a clean docs rebuild without running the
full ` + "`stratt clean`" + ` (which also drops .venv, caches, etc.).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			report := detect.Scan(cwd)

			var targets []string
			for _, s := range report.Stacks {
				switch s.Name {
				case "mkdocs":
					targets = append(targets, filepath.Join(cwd, "site"))
				case "sphinx":
					targets = append(targets,
						filepath.Join(cwd, "docs", "_build"),
						filepath.Join(cwd, "docs", "_autosummary"),
					)
				case "hugo":
					if src := detect.FindHugoSource(cwd); src != "" {
						targets = append(targets, filepath.Join(cwd, src, "public"))
					}
				}
			}
			if len(targets) == 0 {
				return errNoDocsToolchain
			}
			for _, p := range targets {
				if err := os.RemoveAll(p); err != nil {
					return fmt.Errorf("rm -rf %s: %w", p, err)
				}
				rel, _ := filepath.Rel(cwd, p)
				if rel == "" {
					rel = p
				}
				fmt.Fprintf(out, "removed %s\n", rel)
			}
			return nil
		},
	}
}

// errNoDocsToolchain is returned by `stratt docs clean` when no docs
// stack is detected.
var errNoDocsToolchain = errors.New("no docs toolchain detected (looked for mkdocs.yml, docs/conf.py, or hugo.{toml,yaml,yml,json})")

func newDocsActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: fmt.Sprintf("%s docs using the detected toolchain", action),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			tool, argv, err := docsCommand(cwd, action)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "→ %s %v\n", tool, argv)
			c := exec.CommandContext(cmd.Context(), tool, argv...)
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			c.Dir = cwd
			return c.Run()
		},
	}
}

// docsCommand returns (tool, args) for the given action against whichever
// docs toolchain is detected.  Returns an error when no docs stack is
// detected.
func docsCommand(root, action string) (string, []string, error) {
	report := detect.Scan(root)
	for _, s := range report.Stacks {
		switch s.Name {
		case "mkdocs":
			switch action {
			case "build":
				return "mkdocs", []string{"build"}, nil
			case "serve":
				return "mkdocs", []string{"serve"}, nil
			}
		case "sphinx":
			switch action {
			case "build":
				return "sphinx-build", []string{"docs", "_build/html"}, nil
			case "serve":
				return "sphinx-autobuild", []string{"docs", "_build/html"}, nil
			}
		case "hugo":
			return hugoCommand(root, action)
		}
	}
	return "", nil, errors.New("no docs toolchain detected (looked for mkdocs.yml, docs/conf.py, or hugo.{toml,yaml,yml,json})")
}

// hugoCommand builds the right Hugo invocation given where the site's
// config lives.  When hugo.toml is in a subdirectory (e.g. `docs/`),
// passes --source so Hugo uses that as its project root; otherwise runs
// from cwd.
func hugoCommand(root, action string) (string, []string, error) {
	src := detect.FindHugoSource(root)
	var argv []string
	if src != "" && src != "." {
		argv = append(argv, "--source", src)
	}
	switch action {
	case "build":
		argv = append(argv, "--minify")
		return "hugo", argv, nil
	case "serve":
		// `hugo server` is the canonical incantation; `hugo serve` is
		// an alias in newer versions but `server` works everywhere.
		argv = append([]string{"server"}, argv...)
		return "hugo", argv, nil
	}
	return "", nil, errors.New("unknown hugo action: " + action)
}
