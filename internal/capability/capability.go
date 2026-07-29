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
	"path/filepath"

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
//
// A Resolver may also be a repo root: when a monorepo keeps its
// stacks in subdirectories (e.g. backend/ + frontend/), subprojects holds one
// child Resolver per stack-bearing directory and the per-stack verbs fan
// out across them.  subprojects is empty for ordinary single-stack repos, in
// which case the resolver behaves exactly as it always has.
type Resolver struct {
	root        string
	report      detect.Report
	subprojects []subproject
}

// subproject is one workspace subdirectory with its own resolver.
type subproject struct {
	dir  string          // relative to the repo root, e.g. "backend"
	only map[string]bool // if non-nil, only these verbs fan out here
	skip map[string]bool // if non-nil, these verbs do NOT fan out here
	res  *Resolver
}

// SubprojectSpec is an explicitly-declared subproject, translated from
// [[subprojects]] in the project config.  Passing specs to
// NewWithSubprojects opts a repo into fan-out even when its root carries a
// stack, and lets callers pin the subproject set, its order, and a
// per-subproject verb filter (Only is an allowlist, Skip a denylist;
// callers must not set both).
type SubprojectSpec struct {
	Dir  string   // path relative to root
	Only []string // if non-empty, only these verbs fan out to this subproject
	Skip []string // if non-empty, these verbs do NOT fan out to this subproject
}

// SubprojectView is a read-only summary of a subproject for `stratt
// doctor` / `agents context` display.
type SubprojectView struct {
	Dir    string
	Stacks []detect.Stack
}

// primaryStacks are the language/toolchain stacks that, when present at
// the repo root, mean stratt operates root-first (today's behavior) and
// does NOT auto-detect subdirectory subprojects.  The auxiliary stacks
// (docker, kustomize, the docs generators, github-actions) deliberately
// don't count: a repo whose root holds only a Dockerfile and kustomize
// overlays — a deploy-only root, as in a two-service monorepo — still
// auto-detects its backend/ and frontend/ subprojects.  Explicit [[subprojects]]
// config overrides this gate entirely.
var primaryStacks = map[string]bool{
	"go":                 true,
	"node+npm":           true,
	"python+uv":          true,
	"php":                true,
	"ansible-collection": true,
	"ansible-role":       true,
	"ansible-playbook":   true,
}

// New returns a Resolver scoped to root with automatic workspace
// detection.  Detection runs once at construction time; subsequent
// Resolve calls are cheap lookups.
func New(root string) *Resolver {
	return NewWithSubprojects(root, nil)
}

// NewWithSubprojects returns a Resolver scoped to root, with subprojects
// resolved from declared (the explicit [[subprojects]] config) when non-empty,
// or auto-detected otherwise.  Auto-detection only kicks in when the root
// has no primary language stack, so single-stack repos are unaffected.
func NewWithSubprojects(root string, declared []SubprojectSpec) *Resolver {
	r := &Resolver{root: root, report: detect.Scan(root)}

	var reports []detect.SubprojectReport
	only := map[string]map[string]bool{}
	skip := map[string]map[string]bool{}
	switch {
	case len(declared) > 0:
		for _, spec := range declared {
			reports = append(reports, detect.SubprojectReport{
				Dir:    spec.Dir,
				Report: detect.Scan(filepath.Join(root, spec.Dir)),
			})
			if s := verbSet(spec.Only); s != nil {
				only[spec.Dir] = s
			}
			if s := verbSet(spec.Skip); s != nil {
				skip[spec.Dir] = s
			}
		}
	case !hasPrimaryStack(r.report):
		reports = detect.ScanRepo(root).Subprojects
	}

	for _, mr := range reports {
		// A declared subproject that resolves no stacks (typo'd path, or a
		// dir with nothing detectable) contributes nothing; skip it
		// rather than fanning out an empty engine.
		if len(mr.Report.Stacks) == 0 {
			continue
		}
		dir := mr.Dir
		r.subprojects = append(r.subprojects, subproject{
			dir:  dir,
			only: only[dir],
			skip: skip[dir],
			res:  &Resolver{root: filepath.Join(root, dir), report: mr.Report},
		})
	}
	return r
}

// verbSet turns a verb list into a lookup set, or nil for an empty list
// (so callers can treat nil as "no filter").
func verbSet(verbs []string) map[string]bool {
	if len(verbs) == 0 {
		return nil
	}
	set := make(map[string]bool, len(verbs))
	for _, v := range verbs {
		set[v] = true
	}
	return set
}

// hasPrimaryStack reports whether the report contains any primary
// language/toolchain stack (see primaryStacks).
func hasPrimaryStack(report detect.Report) bool {
	for _, s := range report.Stacks {
		if primaryStacks[s.Name] {
			return true
		}
	}
	return false
}

// Stacks returns the detected stacks at the repo root.  For a
// monorepo whose code lives in subdirectories this is empty (or just the
// root's auxiliary stacks); see Subprojects for the per-subdirectory stacks.
func (r *Resolver) Stacks() []detect.Stack {
	return r.report.Stacks
}

// Subprojects returns a read-only view of the subprojects, in resolved
// order.  Empty for single-stack repos.
func (r *Resolver) Subprojects() []SubprojectView {
	out := make([]SubprojectView, 0, len(r.subprojects))
	for _, m := range r.subprojects {
		out = append(out, SubprojectView{Dir: m.dir, Stacks: m.res.report.Stacks})
	}
	return out
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
// `style`, `reset`, and `all` are composite built-ins — their resolveXxx
// returns a compositeEngine whose Run() is intentionally inert; execution
// flows through the task Registry, which expands the composition into a
// Task with a populated Tasks field.
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
	"reset",
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
	case "reset":
		res.Engine = r.resolveReset()
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
	res.Engine = r.fanOut(command, res.Engine, func(m *Resolver) Engine {
		return m.Resolve(command).Engine
	})
	return res
}

// fanOutVerbs are the per-stack toolchain commands that fan out across
// subprojects.  The repo-global verbs (release, deploy, docs,
// clean) deliberately aren't here — they resolve against the root, so a
// monorepo's single kustomize deploy / release flow keeps working.  The
// composites (style, all) aren't listed either: they expand through the
// task registry into these leaf verbs, so they fan out transitively.
var fanOutVerbs = map[string]bool{
	"build":   true,
	"test":    true,
	"lint":    true,
	"format":  true,
	"setup":   true,
	"sync":    true,
	"lock":    true,
	"upgrade": true,
}

// fanOut wraps a root-resolved engine into a fanOutEngine when this
// resolver has subprojects and command is a per-stack verb.  resolveMember
// produces each subproject's engine for command (a small indirection so the
// caller controls whether subprojects resolve the fixing or check-only lint
// variant).
//
// For single-stack repos (no subprojects) or repo-global verbs it returns
// rootEngine untouched, so existing resolution is byte-for-byte
// preserved.  When subprojects are active the root's own per-stack engine is
// intentionally excluded — declare `path = "."` in [[subprojects]] to fold the
// root back in as a subproject.
func (r *Resolver) fanOut(command string, rootEngine Engine, resolveMember func(*Resolver) Engine) Engine {
	if len(r.subprojects) == 0 || !fanOutVerbs[command] {
		return rootEngine
	}
	var parts []subprojectStep
	for _, m := range r.subprojects {
		if m.only != nil && !m.only[command] {
			continue
		}
		if m.skip != nil && m.skip[command] {
			continue
		}
		eng := resolveMember(m.res)
		if eng == nil {
			continue
		}
		setEngineDir(eng, m.res.root)
		parts = append(parts, subprojectStep{dir: m.dir, engine: eng})
	}
	if len(parts) == 0 {
		return nil
	}
	return &fanOutEngine{command: command, subprojects: parts}
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
