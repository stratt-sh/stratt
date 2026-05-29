package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/workspace"
)

// newCloneCmd implements `stratt clone`: a thin wrapper around
// `git clone` that drops the new repo into a deterministic location
// under the user's configured workspace root.
//
// Usage:
//
//	stratt clone <url> [git-clone-flags...]
//	stratt clone <url> <explicit-target> [git-clone-flags...]
//
// In the second form, stratt does not compute the target — whatever the
// user passes goes straight through to git.  This is the escape hatch.
func newCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "clone <url> [target] [-- git-clone-flags...]",
		Short:              "git clone into the configured workspace layout",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		Long: `Wraps ` + "`git clone`" + ` to place the new repo at a deterministic path
under the workspace root configured in ~/.stratt/config.toml.

Example:
  [workspace]
  root = "~/code"
  layout = "{host}/{org}/{repo}"   # default

  $ stratt clone https://github.com/stratt-sh/stratt
  # clones into ~/code/github.com/stratt-sh/stratt

Pass an explicit target as the second argument to bypass layout resolution.
All other flags are forwarded verbatim to ` + "`git clone`" + `.

On first run with no [workspace] configured, stratt prompts for the root
and layout (when stdin is a terminal) and writes the choices to the
user config file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClone(cmd, args)
		},
	}
	return cmd
}

func runClone(cmd *cobra.Command, args []string) error {
	// DisableFlagParsing means cobra won't catch --help for us.
	if args[0] == "-h" || args[0] == "--help" {
		return cmd.Help()
	}

	url := args[0]
	rest := args[1:]

	// Detect an explicit target: the first positional non-flag after the
	// URL.  Anything starting with "-" is a git flag and forwarded as-is.
	var target string
	var flags []string
	if len(rest) > 0 && len(rest[0]) > 0 && rest[0][0] != '-' {
		target = rest[0]
		flags = rest[1:]
	} else {
		flags = rest
	}

	if target == "" {
		t, err := resolveCloneTarget(cmd, url)
		if err != nil {
			return err
		}
		target = t
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", target, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "cloning into %s\n", target)

	gitArgs := append([]string{"clone"}, flags...)
	gitArgs = append(gitArgs, url, target)
	g := exec.CommandContext(cmd.Context(), "git", gitArgs...)
	g.Stdout = cmd.OutOrStdout()
	g.Stderr = cmd.ErrOrStderr()
	g.Stdin = os.Stdin
	if err := g.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func resolveCloneTarget(cmd *cobra.Command, url string) (string, error) {
	usr, err := config.LoadUser()
	if err != nil {
		return "", err
	}

	if usr == nil || usr.Workspace == nil || usr.Workspace.Root == "" {
		ws, err := setupWorkspaceInteractive(cmd)
		if err != nil {
			return "", err
		}
		usr = &config.User{Workspace: ws}
	}

	remote, err := workspace.ParseRemote(url)
	if err != nil {
		return "", err
	}

	layout := usr.Workspace.Layout
	if layout == "" {
		layout = workspace.DefaultLayout
	}
	return workspace.Resolve(usr.Workspace.Root, layout, remote)
}

// setupWorkspaceInteractive prompts the user for workspace.root and
// workspace.layout, persists the choices to the user config file, and
// returns the freshly-set values.  In non-interactive contexts (CI,
// piped stdin) it returns the same actionable error the previous
// non-prompting flow used to emit.
func setupWorkspaceInteractive(cmd *cobra.Command) (*config.UserWorkspace, error) {
	cfgPath, _ := config.UserConfigPath()
	if !stdinIsTerminal(cmd) {
		return nil, fmt.Errorf(
			"no [workspace] root configured in %s\n"+
				"add one (no terminal available to prompt):\n"+
				"  [workspace]\n  root = \"~/code\"\n  layout = \"{host}/{org}/{repo}\"",
			cfgPath,
		)
	}

	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	fmt.Fprintln(out, "No [workspace] configured. Let's set it up.")
	fmt.Fprintf(out, "(Saving to %s)\n\n", cfgPath)

	root, err := promptRoot(out, in)
	if err != nil {
		return nil, err
	}
	layout, err := promptLayout(out, in)
	if err != nil {
		return nil, err
	}

	if err := config.SetUserWorkspace(cfgPath, root, layout); err != nil {
		return nil, fmt.Errorf("write %s: %w", cfgPath, err)
	}
	fmt.Fprintf(out, "\nSaved [workspace] to %s\n\n", cfgPath)

	return &config.UserWorkspace{Root: root, Layout: layout}, nil
}

func promptRoot(out io.Writer, in *bufio.Reader) (string, error) {
	const def = "~/code"
	fmt.Fprintf(out, "Workspace root [%s]: ", def)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read root: %w", err)
	}
	if s := strings.TrimSpace(line); s != "" {
		return s, nil
	}
	return def, nil
}

var layoutChoices = []struct {
	label   string
	layout  string
	example string
}{
	{"host / org / repo", "{host}/{org}/{repo}", "~/code/github.com/stratt-sh/stratt"},
	{"org / repo", "{org}/{repo}", "~/code/stratt-sh/stratt"},
}

func promptLayout(out io.Writer, in *bufio.Reader) (string, error) {
	fmt.Fprintln(out, "\nLayout:")
	for i, c := range layoutChoices {
		fmt.Fprintf(out, "  %d) %-20s  e.g. %s\n", i+1, c.label, c.example)
	}
	fmt.Fprintf(out, "  %d) custom (enter a template)\n", len(layoutChoices)+1)

	for {
		fmt.Fprintf(out, "Choose [1]: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read layout choice: %w", err)
		}
		s := strings.TrimSpace(line)
		if s == "" {
			return layoutChoices[0].layout, nil
		}
		switch s {
		case "1":
			return layoutChoices[0].layout, nil
		case "2":
			return layoutChoices[1].layout, nil
		case "3":
			return promptCustomLayout(out, in)
		default:
			fmt.Fprintf(out, "  (please enter 1, 2, or 3)\n")
		}
	}
}

func promptCustomLayout(out io.Writer, in *bufio.Reader) (string, error) {
	fmt.Fprintln(out, "  Placeholders: {host}, {org}, {repo}")
	for {
		fmt.Fprintf(out, "  Custom layout: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read custom layout: %w", err)
		}
		s := strings.TrimSpace(line)
		if s == "" {
			fmt.Fprintln(out, "  (layout cannot be empty)")
			continue
		}
		// Validate against a dummy remote so typos fail here, not later.
		if _, err := workspace.Render(s, workspace.Remote{Host: "h", Org: "o", Repo: "r"}); err != nil {
			fmt.Fprintf(out, "  invalid layout: %s\n", err)
			continue
		}
		return s, nil
	}
}

func stdinIsTerminal(cmd *cobra.Command) bool {
	r := cmd.InOrStdin()
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
