package capability

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/stratt-sh/stratt/internal/detect"
)

// cleanEngine removes the conventional build / cache artifacts for every
// stack detected at root.  Unlike most engines it is implemented natively
// (no external tool for the core removal work), so it always resolves and
// always reports Ready.
//
// It lives in capability — not as a bespoke subcommand body in cli — so
// clean participates in the task registry like every other built-in:
// `stratt run clean` works, [tasks.clean] can override/augment/disable
// it, and composites (`reset`) can chain it.
//
// docker opts into pruning dangling images (requirements §3 "clean" #4);
// it is toggled per invocation by the cli layer via SetDockerPrune since
// registry tasks carry no flags.
type cleanEngine struct {
	root   string
	docker bool
	out    io.Writer // progress log; defaults to os.Stdout
}

// CleanEngine is the assertion target the cli layer uses to plumb
// per-invocation options (`--docker`, the command's output writer) into
// the registry-held clean engine instance.
type CleanEngine interface {
	Engine
	SetDockerPrune(on bool)
	SetOutput(w io.Writer)
}

func (e *cleanEngine) SetDockerPrune(on bool) { e.docker = on }
func (e *cleanEngine) SetOutput(w io.Writer)  { e.out = w }

func (e *cleanEngine) Name() string {
	name := "remove build/cache artifacts per detected stacks"
	if e.docker {
		name += " + docker image prune"
	}
	return name
}

func (e *cleanEngine) Status() EngineStatus { return StatusReady }

func (e *cleanEngine) Run(ctx context.Context, _ []string) error {
	out := e.out
	if out == nil {
		out = os.Stdout
	}
	report := detect.Scan(e.root)

	targets := []string{filepath.Join(e.root, ".stratt", "cache")}
	needPycache := false
	needEggInfo := false
	needUVCacheClean := false
	for _, s := range report.Stacks {
		switch s.Name {
		case "go":
			targets = append(targets, filepath.Join(e.root, "bin"))
		case "python+uv":
			targets = append(targets,
				filepath.Join(e.root, ".venv"),
				filepath.Join(e.root, "build"),
				filepath.Join(e.root, "dist"),
				filepath.Join(e.root, ".pytest_cache"),
				filepath.Join(e.root, ".ruff_cache"),
				filepath.Join(e.root, ".coverage"),
				filepath.Join(e.root, "htmlcov"),
			)
			needPycache = true
			needEggInfo = true
			needUVCacheClean = true
		case "mkdocs":
			targets = append(targets, filepath.Join(e.root, "site"))
		case "sphinx":
			targets = append(targets,
				filepath.Join(e.root, "docs", "_build"),
				filepath.Join(e.root, "docs", "_autosummary"),
			)
		case "hugo":
			src := detect.FindHugoSource(e.root)
			if src != "" {
				targets = append(targets, filepath.Join(e.root, src, "public"))
			}
		case "ansible-collection":
			targets = append(targets,
				filepath.Join(e.root, "dist"),
				filepath.Join(e.root, ".ansible"),
				filepath.Join(e.root, "collections"),
			)
		case "ansible-role":
			targets = append(targets, filepath.Join(e.root, ".ansible"))
		case "ansible-playbook":
			targets = append(targets,
				filepath.Join(e.root, ".ansible"),
				filepath.Join(e.root, "collections"),
			)
		}
	}
	for _, p := range targets {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("rm -rf %s: %w", p, err)
		}
		fmt.Fprintf(out, "removed %s\n", relTo(e.root, p))
	}

	if needPycache {
		if err := removePycache(e.root, out); err != nil {
			return err
		}
	}
	if needEggInfo {
		if err := removeEggInfo(e.root, out); err != nil {
			return err
		}
	}
	if needUVCacheClean {
		runUVCacheClean(ctx, out)
	}
	if e.docker {
		if err := e.runDockerPrune(ctx, report, out); err != nil {
			return err
		}
	}
	return nil
}

// runDockerPrune drops dangling docker images (`--docker` opt-in).  The
// user asked for it explicitly, so unlike the best-effort uv cache step a
// failure here is a real error — except when the repo has no docker stack,
// where the flag is simply inapplicable and we say so.
func (e *cleanEngine) runDockerPrune(ctx context.Context, report detect.Report, out io.Writer) error {
	hasDocker := false
	for _, s := range report.Stacks {
		if s.Name == "docker" {
			hasDocker = true
			break
		}
	}
	if !hasDocker {
		fmt.Fprintln(out, "skipped docker image prune (no docker stack detected)")
		return nil
	}
	if !available("docker") {
		return fmt.Errorf("--docker: %q is not installed or not on PATH", "docker")
	}
	cmd := exec.CommandContext(ctx, "docker", "image", "prune", "--force")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker image prune --force: %w", err)
	}
	fmt.Fprintln(out, "pruned dangling docker images")
	return nil
}

// runUVCacheClean drops the global uv download cache.  Best effort:
// missing uv or a non-zero exit logs and continues — clean shouldn't
// fail because of cache cleanup.
func runUVCacheClean(ctx context.Context, out io.Writer) {
	if !available("uv") {
		fmt.Fprintln(out, "skipped uv cache clean (uv not on PATH)")
		return
	}
	c := exec.CommandContext(ctx, "uv", "cache", "clean")
	c.Stdout = out
	c.Stderr = out
	if err := c.Run(); err != nil {
		fmt.Fprintf(out, "uv cache clean: %v (continuing)\n", err)
		return
	}
	fmt.Fprintln(out, "cleaned uv cache")
}

// removeEggInfo walks root and removes *.egg-info directories.
func removeEggInfo(root string, log io.Writer) error {
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

// removePycache walks root and removes every __pycache__ directory.
func removePycache(root string, log io.Writer) error {
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
