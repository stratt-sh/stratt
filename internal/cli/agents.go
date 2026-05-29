package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/runner"
	"github.com/stratt-sh/stratt/internal/workspace"
)

// agentsOrientation is the static "what is stratt / how do I drive it"
// prose printed by `stratt agents context`.  It is intentionally
// repo-independent; the live, repo-specific command map is appended at
// runtime.  Kept as an embedded file so it reads as prose, not a Go
// string literal.
//
//go:embed agents_context.txt
var agentsOrientation string

// newAgentsCmd is the `stratt agents` namespace: everything an AI agent
// needs to discover and drive stratt.  Bare `stratt agents` lists the
// subcommands (Cobra's default for a parent with no Run).
func newAgentsCmd(b BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Help AI agents discover and drive stratt",
		Long: `Commands for making stratt legible to AI coding agents.

  stratt agents context   print orientation + the resolved command map for this repo
  stratt agents init      add a stratt pointer block to AGENTS.md
  stratt agents sync      refresh the stratt block in AGENTS.md`,
	}
	cmd.AddCommand(
		newAgentsContextCmd(b),
		newAgentsInitCmd(),
		newAgentsSyncCmd(),
	)
	return cmd
}

// newAgentsContextCmd prints the one-stop agent dump: static orientation
// followed by the live resolved command map for the current repo.  An
// agent runs this once and has everything it needs to drive stratt here.
func newAgentsContextCmd(b BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "context",
		Short: "Print agent orientation plus the resolved command map for this repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprint(out, agentsOrientation)

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			renderRepoContext(out, cwd, b)
			renderWorkspaceContext(out)
			return nil
		},
	}
}

// renderRepoContext appends the "This repository" section: detected
// stacks and the resolved command map.  Tolerant of config/detection
// problems — like doctor, it surfaces issues inline rather than failing,
// because an agent reading context must still get whatever is knowable.
func renderRepoContext(out interface{ Write([]byte) (int, error) }, cwd string, b BuildInfo) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "THIS REPOSITORY (%s)\n", cwd)

	proj, cfgErr := config.Load(cwd)
	resolver := capability.New(cwd)

	stacks := resolver.Stacks()
	if len(stacks) == 0 {
		fmt.Fprintln(out, "  no recognized stacks detected here — stratt runs in zero-config mode")
		return
	}

	names := make([]string, 0, len(stacks))
	for _, s := range stacks {
		names = append(names, fmt.Sprintf("%s (via %s)", s.Name, s.Signal))
	}
	fmt.Fprintf(out, "  detected stacks: %s\n", joinComma(names))

	var reg *runner.Registry
	if cfgErr == nil {
		reg, _ = runner.BuildRegistry(resolver, proj)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  resolved commands (run `stratt <command>`):")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, rc := range resolveCommandList(resolver, reg) {
		if rc.Backend == "" {
			fmt.Fprintf(tw, "    %s\t→ —\t(no engine matched)\n", rc.Command)
			continue
		}
		fmt.Fprintf(tw, "    %s\t→ %s\t%s\n", rc.Command, rc.Backend, rc.Marker)
	}
	tw.Flush()

	if cfgErr != nil {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  note: project config has an error (%v) — fix it before running commands.\n", cfgErr)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  full health detail: `stratt doctor`")
}

// renderWorkspaceContext appends a WORKSPACE section telling an agent it
// is inside a stratt-managed workspace and how to discover sibling repos.
// It prints nothing unless the user has actually configured a [workspace]
// root — without that, there is no workspace to describe and `stratt
// workspace list` wouldn't work anyway.  Config-load failures are
// swallowed: a missing or broken user config simply means "no workspace
// section", never an error in the agent dump.
func renderWorkspaceContext(out interface{ Write([]byte) (int, error) }) {
	usr, err := config.LoadUser()
	if err != nil || usr == nil || usr.Workspace == nil || usr.Workspace.Root == "" {
		return
	}
	layout := usr.Workspace.Layout
	if layout == "" {
		layout = workspace.DefaultLayout
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "WORKSPACE")
	fmt.Fprintf(out, "  This repo lives in a stratt-managed workspace rooted at %s, laid out\n", usr.Workspace.Root)
	fmt.Fprintf(out, "  as %s.\n", layout)
	fmt.Fprintln(out, "  Run `stratt workspace list` to see the other repos here and their")
	fmt.Fprintln(out, "  paths — handy when a task needs code or context from a sibling repo.")
}

// joinComma joins parts with ", " — a tiny local helper to avoid pulling
// strings.Join into the call sites for one use each.
func joinComma(parts []string) string {
	res := ""
	for i, p := range parts {
		if i > 0 {
			res += ", "
		}
		res += p
	}
	return res
}

// agentsFilePath returns the path to the repo's AGENTS.md (always at the
// working directory root, the conventional location).
func agentsFilePath(cwd string) string {
	return filepath.Join(cwd, "AGENTS.md")
}
