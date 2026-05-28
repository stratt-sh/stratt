package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zebpalmer/stratt/internal/detect"
)

// newCleanCmd implements `stratt clean`: walks the detected stacks
// and removes their conventional artifact directories.
func newCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove build / cache artifacts for the detected stacks",
		Long: `Remove conventional build and cache artifacts for every detected stack.

Always removes:
  .stratt/cache/

Per stack:
  go                 → ./bin
  python+uv          → .venv/, build/, dist/, *.egg-info, .pytest_cache/,
                       .ruff_cache/, .coverage, htmlcov/, **/__pycache__, and
                       ` + "`uv cache clean`" + ` to drop the global uv download cache
  ansible-collection → dist/, .ansible/, collections/
  ansible-role       → .ansible/
  ansible-playbook   → .ansible/, collections/
  mkdocs             → site/
  sphinx             → docs/_build/, docs/_autosummary/
  hugo               → <hugo source>/public/

Does not touch Docker images by default (requires --docker explicitly).
After cleaning a python+uv repo, run ` + "`stratt setup`" + ` to rebuild .venv.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			report := detect.Scan(cwd)

			targets := []string{filepath.Join(cwd, ".stratt", "cache")}
			needPycache := false
			needEggInfo := false
			needUVCacheClean := false
			for _, s := range report.Stacks {
				switch s.Name {
				case "go":
					targets = append(targets, filepath.Join(cwd, "bin"))
				case "python+uv":
					targets = append(targets,
						filepath.Join(cwd, ".venv"),
						filepath.Join(cwd, "build"),
						filepath.Join(cwd, "dist"),
						filepath.Join(cwd, ".pytest_cache"),
						filepath.Join(cwd, ".ruff_cache"),
						filepath.Join(cwd, ".coverage"),
						filepath.Join(cwd, "htmlcov"),
					)
					needPycache = true
					needEggInfo = true
					needUVCacheClean = true
				case "mkdocs":
					targets = append(targets, filepath.Join(cwd, "site"))
				case "sphinx":
					targets = append(targets,
						filepath.Join(cwd, "docs", "_build"),
						filepath.Join(cwd, "docs", "_autosummary"),
					)
				case "hugo":
					src := detect.FindHugoSource(cwd)
					if src != "" {
						targets = append(targets, filepath.Join(cwd, src, "public"))
					}
				case "ansible-collection":
					targets = append(targets,
						filepath.Join(cwd, "dist"),
						filepath.Join(cwd, ".ansible"),
						filepath.Join(cwd, "collections"),
					)
				case "ansible-role":
					targets = append(targets, filepath.Join(cwd, ".ansible"))
				case "ansible-playbook":
					targets = append(targets,
						filepath.Join(cwd, ".ansible"),
						filepath.Join(cwd, "collections"),
					)
				}
			}
			for _, p := range targets {
				if err := os.RemoveAll(p); err != nil {
					return fmt.Errorf("rm -rf %s: %w", p, err)
				}
				fmt.Fprintf(out, "removed %s\n", relTo(cwd, p))
			}

			if needPycache {
				if err := removePycache(cwd, out); err != nil {
					return err
				}
			}
			if needEggInfo {
				if err := removeEggInfo(cwd, out); err != nil {
					return err
				}
			}
			if needUVCacheClean {
				runUVCacheClean(cmd, out)
			}
			return nil
		},
	}
}

// runUVCacheClean drops the global uv download cache.  Best effort:
// missing uv or a non-zero exit logs and continues — clean shouldn't
// fail because of cache cleanup.
func runUVCacheClean(cmd *cobra.Command, out interface{ Write([]byte) (int, error) }) {
	if _, err := exec.LookPath("uv"); err != nil {
		fmt.Fprintln(out, "skipped uv cache clean (uv not on PATH)")
		return
	}
	c := exec.CommandContext(cmd.Context(), "uv", "cache", "clean")
	c.Stdout = out
	c.Stderr = out
	if err := c.Run(); err != nil {
		fmt.Fprintf(out, "uv cache clean: %v (continuing)\n", err)
		return
	}
	fmt.Fprintln(out, "cleaned uv cache")
}

// removeEggInfo walks cwd and removes *.egg-info directories.
func removeEggInfo(root string, log interface{ Write([]byte) (int, error) }) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".venv", "node_modules":
				return filepath.SkipDir
			}
			if filepath.Ext(info.Name()) == ".egg-info" {
				if rmErr := os.RemoveAll(path); rmErr == nil {
					fmt.Fprintf(log, "removed %s\n", relTo(root, path))
				}
				return filepath.SkipDir
			}
		}
		return nil
	})
}

// removePycache walks cwd and removes every __pycache__ directory.
func removePycache(root string, log interface{ Write([]byte) (int, error) }) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && info.Name() == "__pycache__" {
			if rmErr := os.RemoveAll(path); rmErr == nil {
				fmt.Fprintf(log, "removed %s\n", relTo(root, path))
			}
			return filepath.SkipDir
		}
		return nil
	})
}

func relTo(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}
