package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/bump"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/release"
	"github.com/stratt-sh/stratt/internal/runner"
)

// newReleaseCmd wires the `stratt release` flow.  This is a custom-shape
// command rather than a generic universal subcommand because it accepts
// positional args (`stratt release patch`) and many flags governing the
// interactive prompts and push behavior.
func newReleaseCmd() *cobra.Command {
	var (
		typeFlag       string
		ciFlag         bool
		yesFlag        bool
		branchFlag     string
		remoteFlag     string
		noPushFlag     bool
		skipChecksFlag bool
	)
	cmd := &cobra.Command{
		Use:   "release [patch|minor|major]",
		Short: "Bump version, commit, tag, and (optionally) push",
		Long: `Run the release flow: bump version per [bump]/[tool.bumpversion] config,
commit, tag, and push.  stratt does NOT build release artifacts —
GitHub Actions takes over after the tag is pushed.

Examples:
  stratt release                # interactive: prompt for patch|minor|major
  stratt release patch          # non-interactive shortcut
  stratt release --type=minor   # equivalent
  stratt release patch --ci     # CI mode: no prompts, fail on missing decisions
  stratt release patch --no-push  # local only; print the push command

Release branch resolution (highest precedence first):
  --branch flag  >  [release] branch in stratt.toml  >  auto-detect (main → master)

See requirements R2.4 for the full design.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			// Reconcile positional arg vs --type flag.  They're equivalent.
			kindStr := strings.TrimSpace(typeFlag)
			if len(args) == 1 {
				if kindStr != "" && args[0] != kindStr {
					return fmt.Errorf("conflicting release types: positional %q vs --type=%q", args[0], kindStr)
				}
				kindStr = args[0]
			}

			// Build the task Registry so the pre-release `all` check has
			// somewhere to dispatch.  Errors here (e.g. cycles in user
			// task config) abort before we touch git.
			reg, _, err := loadRegistry(cwd)
			if err != nil {
				return err
			}

			// Load project + user config to pick up [release] settings.
			proj, err := config.Load(cwd)
			if err != nil {
				return err
			}
			usr, _ := config.LoadUser() // user config is best-effort

			// Resolve branch/remote/push.
			//
			// Precedence (highest first):
			//   CLI flag  >  project config  >  user config  >  built-in default
			//
			// Cobra's `Flags().Changed("name")` distinguishes
			// flag-explicitly-set from flag-defaulted, which is what we
			// need so config layers only kick in when the user didn't
			// pass a flag.
			branch := branchFlag
			remote := remoteFlag
			push := !noPushFlag

			// Project layer.
			if proj != nil && proj.Release != nil {
				if !cmd.Flags().Changed("branch") && proj.Release.Branch != "" {
					branch = proj.Release.Branch
				}
				if !cmd.Flags().Changed("remote") && proj.Release.Remote != "" {
					remote = proj.Release.Remote
				}
				if !cmd.Flags().Changed("no-push") && proj.Release.Push != nil {
					push = *proj.Release.Push
				}
			}
			// User layer (only applies when project hasn't set the field
			// AND no CLI flag was passed).
			if usr != nil && usr.Release != nil {
				if !cmd.Flags().Changed("no-push") && usr.Release.Push != nil &&
					(proj == nil || proj.Release == nil || proj.Release.Push == nil) {
					push = *usr.Release.Push
				}
			}

			opts := release.Options{
				CWD:        cwd,
				CI:         ciFlag,
				AssumeYes:  yesFlag,
				Branch:     branch,
				Remote:     remote,
				Push:       push,
				SkipChecks: skipChecksFlag,
				Stdin:      cmd.InOrStdin(),
				Stdout:     cmd.OutOrStdout(),
				Stderr:     cmd.ErrOrStderr(),
				PreReleaseCheck: func(ctx context.Context) error {
					// Skip silently if `all` doesn't exist (e.g., this
					// repo has no format/lint/test detected).  Better
					// than failing loudly for repos that haven't earned
					// the composite.
					if reg.Lookup("all") == nil {
						fmt.Fprintln(cmd.ErrOrStderr(),
							"  (no `all` task in this repo — skipping pre-release checks)")
						return nil
					}
					r := runner.New(runner.Options{
						Stdout:   cmd.OutOrStdout(),
						Stderr:   cmd.ErrOrStderr(),
						CWD:      cwd,
						Registry: reg,
						CI:       ciFlag,
					})
					return r.RunTask(ctx, "all")
				},
			}

			if kindStr != "" {
				k, err := bump.KindFromString(kindStr)
				if err != nil {
					return err
				}
				opts.Kind = k
				opts.HasKind = true
			}

			if err := release.Run(cmd.Context(), opts); err != nil {
				// Surface bump-config errors with extra context about the
				// chain so users know what to add.
				if errors.Is(err, bump.ErrMissingVersion) {
					return fmt.Errorf("%w (check your [tool.bumpversion] files block matches the file's current content)", err)
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeFlag, "type", "", "release type: patch | minor | major (alternative to positional arg)")
	cmd.Flags().BoolVar(&ciFlag, "ci", false, "non-interactive mode: no prompts, fail loudly on missing decisions")
	cmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "skip final confirmation (major-bump gate still requires explicit input)")
	cmd.Flags().StringVar(&branchFlag, "branch", "", "release branch (default: auto-detect main → master, or [release] branch from config)")
	cmd.Flags().StringVar(&remoteFlag, "remote", "origin", "git remote for push")
	cmd.Flags().BoolVar(&noPushFlag, "no-push", false, "do not push commit/tag to remote (default is to push)")
	cmd.Flags().BoolVar(&skipChecksFlag, "skip-checks", false, "skip the `stratt all` pre-release verification (emergency use only)")
	return cmd
}
