package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/ui"
)

// newInitCmd wires the top-level `stratt init` onboarding flow.  It walks
// a repo through first-time setup one decision at a time, reusing the same
// primitives as the granular commands (`config init`, `agents init`) so
// there's a single source of truth for what gets written.
//
// Every step is offered separately and skipped when already satisfied, so
// `stratt init` is safe to re-run.  --yes accepts each prompt's default
// for non-interactive use (CI, scripted bootstraps).
func newInitCmd(b BuildInfo) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up stratt in this repo (interactive onboarding)",
		Long: `Walk through first-time setup for the current repository:

  • create a starter stratt.toml (or add [tool.stratt] to pyproject.toml)
  • add the stratt pointer block to AGENTS.md for coding agents

Each step is offered separately and skipped if it's already done, so
re-running is safe.  With --yes, accept the default for every prompt
(good for scripts and CI).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return runInit(cmd, cwd, b, yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept the default for every prompt")
	return cmd
}

// prompter carries the shared stdin reader and output writer through the
// onboarding steps.  The reader MUST be created once and reused: a fresh
// bufio.Reader per question buffers (and so swallows) all remaining piped
// input, leaving later prompts to read EOF and silently take their default.
type prompter struct {
	out     io.Writer
	in      *bufio.Reader
	st      *ui.Style
	autoYes bool
}

func runInit(cmd *cobra.Command, cwd string, b BuildInfo, autoYes bool) error {
	p := &prompter{
		out:     cmd.OutOrStdout(),
		in:      bufio.NewReader(cmd.InOrStdin()),
		st:      styleFrom(cmd.Context()),
		autoYes: autoYes,
	}
	fmt.Fprintf(p.out, "%s\n\n", p.st.Bold("stratt init — repo onboarding"))

	if err := initConfigStep(p, cwd, b); err != nil {
		return err
	}
	if err := initAgentsStep(p, cwd); err != nil {
		return err
	}

	fmt.Fprintf(p.out, "\n%s Done. Run %s to see the resolved command map for this repo.\n",
		p.st.Green("✓"), p.st.Bold("stratt help"))
	return nil
}

// initConfigStep offers to create project config, choosing between a
// standalone stratt.toml and a [tool.stratt] block in an existing
// pyproject.toml.  No-ops when config already exists.
func initConfigStep(p *prompter, cwd string, b BuildInfo) error {
	proj, err := config.Load(cwd)
	if err != nil {
		// A conflict or parse error is the user's to resolve — don't
		// bulldoze it with a fresh file.
		fmt.Fprintf(p.out, "%s config: %v — leaving it alone\n", p.st.Yellow("•"), err)
		return nil
	}
	if proj.Source != "" {
		fmt.Fprintf(p.out, "%s config: already present in %s — skipping\n",
			p.st.Green("✓"), relTo(cwd, proj.Source))
		return nil
	}

	create, err := p.ask("Create a starter stratt config?", true)
	if err != nil {
		return err
	}
	if !create {
		fmt.Fprintln(p.out, "  · skipped project config")
		return nil
	}

	// Default to a standalone stratt.toml; offer pyproject only when one
	// already exists (we won't conjure a pyproject.toml for non-Python repos).
	target := filepath.Join(cwd, "stratt.toml")
	pyPath := filepath.Join(cwd, "pyproject.toml")
	if _, statErr := os.Stat(pyPath); statErr == nil {
		usePy, err := p.ask("  Use pyproject.toml ([tool.stratt]) instead of a separate stratt.toml?", false)
		if err != nil {
			return err
		}
		if usePy {
			target = pyPath
		}
	}

	if filepath.Base(target) == "pyproject.toml" {
		if err := appendPyprojectStratt(target, b); err != nil {
			return err
		}
		fmt.Fprintf(p.out, "%s config: added [tool.stratt] to %s\n", p.st.Green("✓"), relTo(cwd, target))
	} else {
		if err := os.WriteFile(target, []byte(defaultConfigTemplate(b)), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(p.out, "%s config: wrote %s\n", p.st.Green("✓"), relTo(cwd, target))
	}
	return nil
}

// initAgentsStep offers to add the stratt pointer block to AGENTS.md,
// creating the file if absent.  No-ops when the block already exists.
func initAgentsStep(p *prompter, cwd string) error {
	path := agentsFilePath(cwd)
	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	content := string(data)
	exists := readErr == nil

	if hasManagedBlock(content) {
		fmt.Fprintf(p.out, "%s agents: AGENTS.md already has a stratt block — skipping\n", p.st.Green("✓"))
		return nil
	}

	q := "Add the stratt pointer block to AGENTS.md?"
	if !exists {
		q = "Create AGENTS.md with the stratt pointer block?"
	}
	add, err := p.ask(q, true)
	if err != nil {
		return err
	}
	if !add {
		fmt.Fprintln(p.out, "  · skipped AGENTS.md")
		return nil
	}

	updated, _ := upsertManagedBlock(content)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return err
	}
	if exists {
		fmt.Fprintf(p.out, "%s agents: added stratt block to AGENTS.md\n", p.st.Green("✓"))
	} else {
		fmt.Fprintf(p.out, "%s agents: created AGENTS.md with stratt block\n", p.st.Green("✓"))
	}
	return nil
}

// appendPyprojectStratt appends a minimal [tool.stratt] table to an
// existing pyproject.toml.  It appends rather than round-tripping the
// whole document (as config.SetRequiredStratt does) so the user's
// existing comments and key ordering survive untouched.
func appendPyprojectStratt(path string, b BuildInfo) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	pin := `# required_stratt = ">= 0.1.0"`
	if b.Version != "" && b.Version != "dev" {
		pin = fmt.Sprintf("required_stratt = %q", ">= "+b.Version)
	}
	block := "\n[tool.stratt]\n" +
		"# stratt project configuration — see https://stratt.sh\n" +
		"# Sections mirror stratt.toml under this namespace, e.g.\n" +
		"# [tool.stratt.tasks.test] with run = \"...\".  `stratt config show` prints the result.\n" +
		pin + "\n"

	body := string(data)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body+block), 0o644)
}

// ask poses a yes/no question and returns the answer.  defaultYes sets the
// [Y/n] vs [y/N] hint and the answer for empty input or EOF.  When the
// prompter is in autoYes mode the prompt is not read and defaultYes is
// returned, so the flow runs unattended.
func (p *prompter) ask(question string, defaultYes bool) (bool, error) {
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	if p.autoYes {
		fmt.Fprintf(p.out, "%s %s (auto)\n", question, hint)
		return defaultYes, nil
	}

	fmt.Fprintf(p.out, "%s %s ", question, hint)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		// EOF with nothing typed (e.g. piped/non-interactive): take the default.
		fmt.Fprintln(p.out)
		return defaultYes, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		// Empty or unrecognized → default.
		return defaultYes, nil
	}
}
