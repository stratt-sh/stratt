// Package capability resolves stratt's universal commands (build, test,
// lint, format, release, deploy, ...) to concrete backend engines per the
// engine resolution chains in requirements.md §3.
//
// The core principle (§0): the user types `stratt test` regardless of
// language or toolchain.  Detection picks the backend; the user never
// names it.  This package is where that mapping happens.
package capability

import (
	"context"
	"os/exec"

	"github.com/stratt-sh/stratt/internal/detect"
)

// Engine is a concrete backend for one universal command in one repo.
//
// An Engine knows its display name (for `stratt doctor`), its readiness
// status, and how to run itself.
type Engine interface {
	// Name returns a human-readable rendering of what this engine will
	// invoke, e.g. "uv run pytest" or "go test ./...".  Used by
	// `stratt doctor` to make backend resolution visible.
	Name() string

	// Status reports the engine's readiness for `stratt doctor`.
	Status() EngineStatus

	// Run executes the engine.  args is reserved for per-command
	// parameters (e.g., `stratt deploy <env> <version>`).  Most engines
	// ignore args.
	Run(ctx context.Context, args []string) error
}

// EngineStatus is the readiness summary surfaced in `stratt doctor`.
type EngineStatus int

const (
	// StatusReady — the engine is implemented and its tooling is on PATH.
	StatusReady EngineStatus = iota
	// StatusMissingTool — the engine is implemented but its underlying
	// external tool isn't on PATH.  Resolves cleanly; fails with a clear
	// error when actually invoked.
	StatusMissingTool
	// StatusPending — the engine is reserved (chain entry exists in the
	// spec) but not yet implemented in stratt.  Reported by `doctor` so
	// users can see what's planned vs. what works today.
	StatusPending
)

// Resolution is the outcome of resolving one universal command in a repo.
// A nil Engine means no detector in the chain matched.
type Resolution struct {
	Command string // e.g. "test"
	Engine  Engine // nil if no chain entry matched
}

// Tooler is an optional interface for engines whose readiness depends on
// an external binary being on `$PATH`.  Implementations return the
// binary name (e.g. "hugo", "uv") so callers — typically `stratt
// doctor` — can offer install suggestions when the tool is missing.
//
// Engines that aren't tool-backed (composites, delegates, not-yet-
// implemented placeholders) should not implement this interface, or
// should return "".
type Tooler interface {
	Tool() string
}

// MultiTooler is an optional interface for engines that wrap several
// tools at once (multiEngine).  Doctor prefers Tools() over Tool()
// when present so every missing dependency in a fan-out engine
// surfaces in the install-hint list.
type MultiTooler interface {
	Tools() []string
}

// Resolver walks the resolution chains for a given repo and answers
// "what engine handles `stratt <command>` here?"
type Resolver struct {
	root   string
	report detect.Report
}

// New returns a Resolver scoped to root.  Detection runs once at
// construction time; subsequent Resolve calls are cheap lookups.
func New(root string) *Resolver {
	return &Resolver{
		root:   root,
		report: detect.Scan(root),
	}
}

// Stacks returns the detected stacks for this repo.
func (r *Resolver) Stacks() []detect.Stack {
	return r.report.Stacks
}

// HasStack reports whether the named stack is present in this repo.
// Used by chain predicates.
func (r *Resolver) HasStack(name string) bool {
	for _, s := range r.report.Stacks {
		if s.Name == name {
			return true
		}
	}
	return false
}

// UniversalCommands is the canonical list of stratt's universal commands,
// in the order they should appear in `stratt doctor` output.
//
// Adding a command here without adding it to ResolveAll is a programming
// error; the resolver will return an "unknown command" Resolution.
//
// `style` and `all` are composite built-ins — their resolveXxx returns
// a compositeEngine whose Run() is intentionally inert; execution flows
// through the task Registry, which expands the composition into a Task
// with a populated Tasks field.
var UniversalCommands = []string{
	"build",
	"test",
	"lint",
	"format",
	"style",
	"setup",
	"sync",
	"lock",
	"upgrade",
	"clean",
	"release",
	"deploy",
	"docs",
	"all",
}

// Resolve returns the chain-resolved Engine for one universal command,
// or a Resolution with Engine == nil if no chain entry matched.
func (r *Resolver) Resolve(command string) Resolution {
	res := Resolution{Command: command}
	switch command {
	case "build":
		res.Engine = r.resolveBuild()
	case "test":
		res.Engine = r.resolveTest()
	case "lint":
		res.Engine = r.resolveLint()
	case "format":
		res.Engine = r.resolveFormat()
	case "setup":
		res.Engine = r.resolveSetup()
	case "sync":
		res.Engine = r.resolveSync()
	case "lock":
		res.Engine = r.resolveLock()
	case "upgrade":
		res.Engine = r.resolveUpgrade()
	case "clean":
		res.Engine = r.resolveClean()
	case "release":
		res.Engine = r.resolveRelease()
	case "deploy":
		res.Engine = r.resolveDeploy()
	case "docs":
		res.Engine = r.resolveDocs()
	case "style":
		res.Engine = r.resolveStyle()
	case "all":
		res.Engine = r.resolveAll()
	}
	return res
}

// ResolveAll resolves every universal command and returns the list
// in UniversalCommands order.  This is the input to `stratt doctor`.
func (r *Resolver) ResolveAll() []Resolution {
	out := make([]Resolution, 0, len(UniversalCommands))
	for _, c := range UniversalCommands {
		out = append(out, r.Resolve(c))
	}
	return out
}

// available is a small helper: does this tool exist on $PATH?
func available(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}
