package cli

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/update"
)

// newSelfCmd wires the `stratt self` subcommand group:
// `stratt self update`, `rollback`, `verify`, `check`.  Per R4.
func newSelfCmd(b BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self",
		Short: "Manage the stratt binary itself (update, rollback, verify)",
	}
	cmd.AddCommand(newSelfUpdateCmd(b))
	cmd.AddCommand(newSelfRollbackCmd(b))
	cmd.AddCommand(newSelfVerifyCmd(b))
	cmd.AddCommand(newSelfCheckCmd(b))
	return cmd
}

// strattUpstreamRepo is compiled into the binary as the trust pin
// (R4.4).  Forks should override this in cmd/stratt/main.go.
const strattUpstreamRepo = "stratt-sh/stratt"

// strattBrewFormula is the fully-qualified Homebrew formula name.
// Used by the update notifier and by `stratt self update` when the
// binary is brew-managed to suggest (and optionally invoke) the right
// `brew upgrade` command.  Forks should override this alongside
// strattUpstreamRepo.
const strattBrewFormula = "stratt-sh/tap/stratt"

func newSelfUpdateCmd(b BuildInfo) *cobra.Command {
	var (
		channel  string
		noVerify bool
		yes      bool
		ciFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Download, verify, and install the latest stratt",
		Long: `Self-update the stratt binary.

For direct-install users, this downloads the latest release, verifies its
Sigstore attestation, and atomically swaps in the new binary.

For Homebrew-installed users (where stratt cannot overwrite its own
binary safely), this checks whether a newer version exists and offers
to run ` + "`brew upgrade " + strattBrewFormula + "`" + ` on your behalf.

The update is gated on:
  - Not running in CI ($CI / $GITHUB_ACTIONS)
  - The latest release being strictly newer than the running version
  - Attestation verification succeeding (direct-install path; R4.3)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Brew-managed binaries get the brew-flavored flow.
			kind, _ := update.DetectInstall()
			if kind == update.InstallHomebrew {
				return runBrewSelfUpdate(cmd, b, channel, yes, ciFlag)
			}

			res, err := update.Apply(cmd.Context(), update.Options{
				Repo:             strattUpstreamRepo,
				Channel:          channel,
				CurrentVersion:   b.Version,
				Stdout:           cmd.OutOrStdout(),
				Stderr:           cmd.ErrOrStderr(),
				SkipVerification: noVerify,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nUpdated stratt %s → %s\nPrevious binary preserved at %s\n",
				res.PreviousVersion, res.NewVersion, res.BackupPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "stable", "release channel: stable | prerelease")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip attestation verification (UNSAFE; for emergency recovery only)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip prompts and auto-accept (currently used on Homebrew installs to run `brew upgrade` without asking)")
	cmd.Flags().BoolVar(&ciFlag, "ci", false, "non-interactive mode: never prompt; on Homebrew, just print the upgrade command")
	return cmd
}

// runBrewSelfUpdate is the Homebrew-specific path: check for a new
// release, suggest the equivalent `brew upgrade` command, and offer to
// run it for the user.  In --ci mode just prints the command without
// prompting.
func runBrewSelfUpdate(cmd *cobra.Command, b BuildInfo, channel string, yes, ciFlag bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	latest, newer, err := update.CheckOnly(ctx, update.Options{
		Repo:           strattUpstreamRepo,
		Channel:        channel,
		CurrentVersion: b.Version,
	})
	if err != nil {
		return fmt.Errorf("check for update: %w", err)
	}
	if !newer {
		fmt.Fprintf(out, "Already up to date (%s).\n", b.Version)
		return nil
	}

	brewCmd := fmt.Sprintf("brew upgrade %s", strattBrewFormula)
	fmt.Fprintf(out,
		"stratt %s is available (you have %s).\n\n"+
			"This binary is managed by Homebrew, so stratt can't update it directly.\n"+
			"The equivalent command is:\n"+
			"  %s\n\n",
		latest.TagName, b.Version, brewCmd)

	// In CI, just print and exit successfully.  The user (or their
	// automation) can decide what to do with the information.
	if ciFlag {
		return nil
	}

	// Prompt unless --yes.
	run := yes
	if !run {
		fmt.Fprint(out, "Run that now? [Y/n] ")
		r := bufio.NewReader(cmd.InOrStdin())
		line, err := r.ReadString('\n')
		if err != nil {
			// EOF or closed stdin → treat as "no", exit cleanly.
			fmt.Fprintln(out)
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			run = true
		}
	}
	if !run {
		return nil
	}

	// Exec brew with stdio passthrough so the user sees its output live.
	fmt.Fprintf(out, "→ %s\n", brewCmd)
	bin, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf("brew not found on PATH (which is surprising on a brew-managed install): %w", err)
	}
	exe := exec.CommandContext(ctx, bin, "upgrade", strattBrewFormula)
	exe.Stdin = cmd.InOrStdin()
	exe.Stdout = out
	exe.Stderr = cmd.ErrOrStderr()
	if err := exe.Run(); err != nil {
		return fmt.Errorf("brew upgrade failed: %w", err)
	}
	fmt.Fprintf(out, "\n✓ Upgraded via Homebrew.\n")
	return nil
}

func newSelfRollbackCmd(b BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Revert to the previously installed stratt binary",
		Long: `Restore the binary from the cache populated by the last successful
` + "`stratt self update`" + `.  Returns an error if no prior version is recorded.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			restored, err := update.Rollback(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rolled back to %s\n", restored)
			return nil
		},
	}
}

func newSelfVerifyCmd(b BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Re-verify the running stratt binary against its release attestation",
		Long: `Re-runs the attestation check against the on-disk binary.  Useful for
catching post-install tampering (disk corruption, malicious overwrite,
etc).

NOT a substitute for first-install trust.  A compromised binary can
trivially fake its own verification — bootstrap trust with an
independent verifier (e.g. ` + "`gh attestation verify`" + ` against the release
archive) before running stratt the first time.  The install script at
https://stratt.sh/install.sh does this automatically when gh is on PATH.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := update.VerifyCurrent(cmd.Context(), update.Options{
				Repo:           strattUpstreamRepo,
				CurrentVersion: b.Version,
				Stderr:         cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Attestation OK.")
			return nil
		},
	}
}

func newSelfCheckCmd(b BuildInfo) *cobra.Command {
	var channel string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether a newer stratt release is available (does not install)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			latest, newer, err := update.CheckOnly(cmd.Context(), update.Options{
				Repo:           strattUpstreamRepo,
				Channel:        channel,
				CurrentVersion: b.Version,
			})
			if err != nil {
				return err
			}
			if !newer {
				fmt.Fprintf(cmd.OutOrStdout(), "Up to date (%s).\n", b.Version)
				return nil
			}
			kind, _ := update.DetectInstall()
			switch kind {
			case update.InstallHomebrew:
				fmt.Fprintf(cmd.OutOrStdout(),
					"Update available: %s — run `brew upgrade %s` (or `stratt self update`, which dispatches to brew)\n",
					latest.TagName, strattBrewFormula)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Update available: %s — run `stratt self update`\n", latest.TagName)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "stable", "release channel: stable | prerelease")
	return cmd
}
