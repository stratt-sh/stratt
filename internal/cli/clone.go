package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
  protocol = "ssh"                 # or "https"

  $ stratt clone https://github.com/stratt-sh/stratt
  # clones git@github.com:stratt-sh/stratt.git into ~/code/github.com/stratt-sh/stratt

A URL that already ends in .git is treated as the canonical clone URL and
used verbatim. A bare URL (no .git) is rewritten to your preferred
protocol from [workspace].protocol; stratt prompts for it on first use.

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

	cloneURL, target, err := resolveClone(cmd, url, target)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", target, err)
	}

	fmt.Fprint(cmd.OutOrStdout(), styleFrom(cmd.Context()).Progress("cloning into "+target))

	gitArgs := append([]string{"clone"}, flags...)
	gitArgs = append(gitArgs, cloneURL, target)
	g := exec.CommandContext(cmd.Context(), "git", gitArgs...)
	g.Stdout = cmd.OutOrStdout()
	g.Stderr = cmd.ErrOrStderr()
	g.Stdin = os.Stdin
	if err := g.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

// resolveClone determines the URL to hand to `git clone` and the
// directory it should land in.
//
//   - An explicit target (the second positional arg) is used verbatim.
//   - A URL already ending in `.git` is the user's signal that they
//     chose the protocol on purpose, so it is cloned as-is.
//   - A bare URL is rewritten to the user's preferred protocol from
//     [workspace].protocol, prompting and saving on first use.
//
// Config (and any first-run prompting) is only touched when actually
// needed: a computed target needs root/layout; a URL rewrite needs the
// protocol.  The explicit-target + `.git` form requires neither.
func resolveClone(cmd *cobra.Command, rawURL, explicitTarget string) (cloneURL, target string, err error) {
	needTarget := explicitTarget == ""
	needRewrite := !endsWithGit(rawURL)

	if !needTarget && !needRewrite {
		return rawURL, explicitTarget, nil
	}

	usr, err := config.LoadUser()
	if err != nil {
		return "", "", err
	}
	ws := usr.Workspace

	remote, parseErr := workspace.ParseRemote(rawURL)
	// A computed target requires a parseable remote — preserve the old
	// hard failure.  For a rewrite-only case, an unparseable URL just
	// falls back to cloning it verbatim.
	if needTarget && parseErr != nil {
		return "", "", parseErr
	}
	if needRewrite && parseErr != nil {
		needRewrite = false
	}

	// First-run workspace setup, only when we need a layout.
	if needTarget && (ws == nil || ws.Root == "") {
		ws, err = setupWorkspaceInteractive(cmd)
		if err != nil {
			return "", "", err
		}
	}

	target = explicitTarget
	if needTarget {
		layout := ws.Layout
		if layout == "" {
			layout = workspace.DefaultLayout
		}
		target, err = workspace.Resolve(ws.Root, layout, remote)
		if err != nil {
			return "", "", err
		}
	}

	cloneURL = rawURL
	if needRewrite {
		proto, err := ensureProtocol(cmd, ws)
		if err != nil {
			return "", "", err
		}
		cloneURL, err = workspace.CloneURL(proto, remote)
		if err != nil {
			return "", "", err
		}
	}

	return cloneURL, target, nil
}

// endsWithGit reports whether a URL already carries a trailing `.git`
// (ignoring a trailing slash), the signal that it is a canonical clone
// URL stratt should pass through untouched.
func endsWithGit(rawURL string) bool {
	return strings.HasSuffix(strings.TrimRight(rawURL, "/"), ".git")
}

// ensureProtocol returns the user's preferred clone protocol, prompting
// for and persisting it on first use.  ws may be nil (no [workspace] at
// all) or carry an empty Protocol; both prompt.  On success the choice
// is written back into ws so a single invocation never prompts twice.
func ensureProtocol(cmd *cobra.Command, ws *config.UserWorkspace) (string, error) {
	if ws != nil && ws.Protocol != "" {
		return ws.Protocol, nil
	}

	cfgPath, _ := config.UserConfigPath()
	if !stdinIsTerminal(cmd) {
		return "", fmt.Errorf(
			"no clone protocol configured in %s\n"+
				"add one (no terminal available to prompt), or pass a URL ending in .git:\n"+
				"  [workspace]\n  protocol = \"ssh\"   # or \"https\"",
			cfgPath,
		)
	}

	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())

	proto, err := promptProtocol(out, in)
	if err != nil {
		return "", err
	}

	if err := config.SetUserWorkspaceProtocol(cfgPath, proto); err != nil {
		return "", fmt.Errorf("write %s: %w", cfgPath, err)
	}
	if ws != nil {
		ws.Protocol = proto
	}
	fmt.Fprint(out, styleFrom(cmd.Context()).Success("Saved clone protocol to "+cfgPath))
	fmt.Fprintln(out)
	return proto, nil
}

var protocolChoices = []struct {
	label    string
	protocol string
	example  string
}{
	{"ssh", workspace.ProtocolSSH, "git@github.com:org/repo.git"},
	{"https", workspace.ProtocolHTTPS, "https://github.com/org/repo.git"},
}

func promptProtocol(out io.Writer, in *bufio.Reader) (string, error) {
	fmt.Fprintln(out, "\nPreferred clone protocol (used for URLs without a trailing .git):")
	for i, c := range protocolChoices {
		fmt.Fprintf(out, "  %d) %-6s e.g. %s\n", i+1, c.label, c.example)
	}
	for {
		fmt.Fprintf(out, "Choose [1]: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read protocol choice: %w", err)
		}
		s := strings.TrimSpace(line)
		if s == "" {
			return protocolChoices[0].protocol, nil
		}
		if n, err := strconv.Atoi(s); err == nil {
			if n >= 1 && n <= len(protocolChoices) {
				return protocolChoices[n-1].protocol, nil
			}
		} else {
			for _, c := range protocolChoices {
				if strings.EqualFold(s, c.label) {
					return c.protocol, nil
				}
			}
		}
		fmt.Fprintf(out, "  (please enter 1 or 2, or ssh/https)\n")
	}
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
	fmt.Fprintln(out)
	fmt.Fprint(out, styleFrom(cmd.Context()).Success("Saved [workspace] to "+cfgPath))
	fmt.Fprintln(out)

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
	{"repo (flat)", "{repo}", "~/code/stratt"},
}

func promptLayout(out io.Writer, in *bufio.Reader) (string, error) {
	fmt.Fprintln(out, "\nLayout:")
	for i, c := range layoutChoices {
		fmt.Fprintf(out, "  %d) %-20s  e.g. %s\n", i+1, c.label, c.example)
	}
	customChoice := len(layoutChoices) + 1
	fmt.Fprintf(out, "  %d) custom (enter a template)\n", customChoice)

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
		if n, err := strconv.Atoi(s); err == nil {
			if n >= 1 && n <= len(layoutChoices) {
				return layoutChoices[n-1].layout, nil
			}
			if n == customChoice {
				return promptCustomLayout(out, in)
			}
		}
		fmt.Fprintf(out, "  (please enter a number from 1 to %d)\n", customChoice)
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
