package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/bump"
	"github.com/stratt-sh/stratt/internal/capability"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/kustomize"
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
		preFlag        string
		ciFlag         bool
		yesFlag        bool
		branchFlag     string
		remoteFlag     string
		noPushFlag     bool
		noCommitFlag   bool
		skipChecksFlag bool
		deployFlag     string
	)
	cmd := &cobra.Command{
		Use:   "release [verb]",
		Short: "Bump version, commit, tag, and (optionally) push",
		Long: `Run the release flow: verify, bump version per [bump]/[tool.bumpversion]
config, commit, tag, and push.

Verbs (the prompt offers the ones valid for your current version):
  patch | minor | major              a normal release
  prepatch | preminor | premajor     start a prerelease (default label "rc")
  iterate                            continue a prerelease (rc.1 → rc.2)
  promote                            finalize a prerelease (drop the suffix)
  relabel <label>                    switch a prerelease's label (resets to .1)

Starting a prerelease accepts three equivalent spellings:
  stratt release preminor            # 0.17.0 → 0.18.0-rc.1
  stratt release minor,rc            # same
  stratt release minor --pre         # same  (--pre=beta for another label)

You confirm the version transition up front, before verification runs — so
you can start a release and walk away rather than returning to a terminal
still waiting on a prompt.  After you confirm, the full ` + "`stratt all`" + ` suite
(sync, format, lint, test, docs) runs, plus a build check that compiles the
project the same way CI will (for goreleaser repos,
` + "`goreleaser build --single-target`" + `), so a broken build fails here instead
of in CI after the tag is pushed.  stratt does NOT produce or publish
release artifacts — GitHub Actions takes over after the tag.

With ` + "`--deploy <env>`" + `, a successful release is immediately followed by a
deploy of the new version to that environment (the same flow as
` + "`stratt deploy <env> <new-version>`" + `): the overlay image tag is bumped,
committed, and pushed.

Examples:
  stratt release                # interactive: prompt for patch|minor|major
  stratt release patch          # non-interactive shortcut
  stratt release --type=minor   # equivalent
  stratt release patch --ci     # CI mode: no prompts, fail on missing decisions
  stratt release patch --no-push  # local only; print the push command
  stratt release patch --deploy prod  # on success, deploy the new version to prod

Release branch resolution (highest precedence first):
  --branch flag  >  [release] branch in stratt.toml  >  auto-detect (main → master)

Full documentation: https://stratt.sh/docs/`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			// Collect verb tokens.  Positional args are the primary form;
			// --type stays a back-compat alias for the base bump when no
			// positional verb is given.
			tokens := append([]string(nil), args...)
			switch {
			case len(tokens) > 0 && strings.TrimSpace(typeFlag) != "":
				return fmt.Errorf("pass the release verb positionally OR via --type, not both")
			case len(tokens) == 0 && strings.TrimSpace(typeFlag) != "":
				tokens = []string{strings.TrimSpace(typeFlag)}
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

			// commit is a tri-state: nil = defer to the bump config's own
			// `commit` setting; non-nil = an explicit override from a CLI
			// flag or config layer.
			var commit *bool
			if cmd.Flags().Changed("no-commit") {
				v := !noCommitFlag
				commit = &v
			}

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
				if commit == nil && proj.Release.Commit != nil {
					commit = proj.Release.Commit
				}
			}
			// User layer (only applies when project hasn't set the field
			// AND no CLI flag was passed).
			if usr != nil && usr.Release != nil {
				if !cmd.Flags().Changed("no-push") && usr.Release.Push != nil &&
					(proj == nil || proj.Release == nil || proj.Release.Push == nil) {
					push = *usr.Release.Push
				}
				if commit == nil && usr.Release.Commit != nil {
					commit = usr.Release.Commit
				}
			}

			// `--deploy <env>` chains a deploy of the freshly-released
			// version.  Validate the inputs up front so a typo or an
			// incompatible flag combination fails before we touch git —
			// not after a successful release+push leaves us half done.
			// `--deploy <env>` chains a deploy of the freshly-released
			// version.  It runs exactly like `stratt deploy <env> <new>`:
			// same defaults (current branch, origin), same [deploy] config
			// for the image.  Validate up front so a typo or incompatible
			// flag fails before we touch git — not after a successful
			// release+push leaves us half done.
			var deployImage string
			if deployFlag != "" {
				if !push {
					return fmt.Errorf("--deploy needs the release pushed to the remote; drop --no-push")
				}
				if commit != nil && !*commit {
					return fmt.Errorf("--deploy needs the release committed; drop --no-commit")
				}
				overlay := kustomize.OverlayPath(cwd, deployFlag)
				if _, err := os.Stat(overlay); err != nil {
					return fmt.Errorf("--deploy %s: no overlay at %s (run `stratt deploy envs` to list environments)", deployFlag, overlay)
				}
				if proj != nil && proj.Deploy != nil {
					deployImage = proj.Deploy.PrimaryImage
				}
			}

			opts := release.Options{
				CWD:        cwd,
				CI:         ciFlag,
				AssumeYes:  yesFlag,
				Style:      styleFrom(cmd.Context()),
				Branch:     branch,
				Remote:     remote,
				Push:       push,
				Commit:     commit,
				SkipChecks: skipChecksFlag,
				Stdin:      cmd.InOrStdin(),
				Stdout:     cmd.OutOrStdout(),
				Stderr:     cmd.ErrOrStderr(),
				PreReleaseCheck: func(ctx context.Context) error {
					r := runner.New(runner.Options{
						Stdout:   cmd.OutOrStdout(),
						Stderr:   cmd.ErrOrStderr(),
						CWD:      cwd,
						Registry: reg,
						CI:       ciFlag,
						Style:    styleFrom(cmd.Context()),
					})

					// Full verification suite (sync/format/lint/test/docs),
					// when this repo has earned the `all` composite.  Skip
					// silently otherwise rather than failing a repo with no
					// format/lint/test detected.
					if reg.Lookup("all") == nil {
						fmt.Fprintln(cmd.ErrOrStderr(),
							"  (no `all` task in this repo — skipping verification suite)")
					} else if err := r.RunTask(ctx, "all"); err != nil {
						return err
					}

					// Build verification: confirm the project builds before
					// we tag, so breakage surfaces here instead of in CI
					// after the tag is pushed.  This produces only a local
					// snapshot (gitignored), never release artifacts — CI
					// still owns producing and publishing those.
					if eng := capability.New(cwd).ResolveBuildVerify(); eng != nil {
						if err := r.RunEngine(ctx, eng, nil); err != nil {
							return err
						}
					}
					return nil
				},
			}

			action, hasAction, err := bump.ParseAction(tokens, preFlag)
			if err != nil {
				return err
			}
			opts.Action = action
			opts.HasAction = hasAction

			if deployFlag != "" {
				opts.PostRelease = func(ctx context.Context, newVersion string) error {
					fmt.Fprintf(cmd.OutOrStdout(), "\n→ deploying %s to %s\n", newVersion, deployFlag)
					return runDeploy(ctx, deployRequest{
						cwd:       cwd,
						env:       deployFlag,
						version:   newVersion,
						image:     deployImage,
						commit:    true,
						push:      true,
						assumeYes: yesFlag || ciFlag,
						remote:    "", // plain deploy defaults: origin
						branch:    "", // plain deploy defaults: current branch
						stdout:    cmd.OutOrStdout(),
						stdin:     cmd.InOrStdin(),
					})
				}
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
	cmd.Flags().StringVar(&typeFlag, "type", "", "base bump: patch | minor | major (back-compat alias for the positional verb)")
	cmd.Flags().StringVar(&preFlag, "pre", "", `make it a prerelease: bare --pre uses "rc"; --pre=beta sets the label`)
	cmd.Flags().Lookup("pre").NoOptDefVal = "rc"
	cmd.Flags().BoolVar(&ciFlag, "ci", false, "non-interactive mode: no prompts, fail loudly on missing decisions")
	cmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "skip final confirmation (major-bump gate still requires explicit input)")
	cmd.Flags().StringVar(&branchFlag, "branch", "", "release branch (default: auto-detect main → master, or [release] branch from config)")
	cmd.Flags().StringVar(&remoteFlag, "remote", "origin", "git remote for push")
	cmd.Flags().BoolVar(&noPushFlag, "no-push", false, "do not push commit/tag to remote (default is to push)")
	cmd.Flags().BoolVar(&noCommitFlag, "no-commit", false, "write the version bump but do not commit, tag, or push (review-then-merge flow)")
	cmd.Flags().BoolVar(&skipChecksFlag, "skip-checks", false, "skip the `stratt all` pre-release verification (emergency use only)")
	cmd.Flags().StringVar(&deployFlag, "deploy", "", "after a successful release, deploy the new version to this environment (e.g. --deploy prod)")
	return cmd
}
