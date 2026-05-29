package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/config"
)

// newConfigCmd wires `stratt config` and its subcommands:
//   - `stratt config migrate`        — apply all auto-fixable deprecations
//   - `stratt config migrate-bump`   — consolidate legacy bump config (R2.4.8)
//   - `stratt config show`           — print the loaded project config
//   - `stratt config require-version` — set required_stratt to current version
func newConfigCmd(b BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and migrate stratt project configuration",
	}
	cmd.AddCommand(newConfigMigrateCmd(b))
	cmd.AddCommand(newConfigMigrateBumpCmd())
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigRequireVersionCmd(b))
	return cmd
}

func newConfigMigrateCmd(b BuildInfo) *cobra.Command {
	var (
		yes         bool
		skipPinBump bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply auto-fixable deprecations to this repo's stratt config",
		Long: `Walk stratt's deprecation registry against the current repo and apply
every auto-fixable migration.  Deprecations that require manual action
are listed but not modified.

After a successful migration, stratt offers to bump
` + "`required_stratt`" + ` to the current binary version so teammates on
older stratt see the explicit pin error rather than confusing
unknown-field errors (R2.3.13).  Pass --no-pin-bump to skip the prompt.

See requirements R2.3.9 for the design.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			fixed, manual, err := config.Migrate(cwd, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nSummary: %d auto-fixed, %d require manual action.\n",
				len(fixed), len(manual))

			// R2.3.13: offer to bump required_stratt after migration.
			if !skipPinBump {
				if err := maybeBumpRequiredStratt(cmd, cwd, b, yes); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "auto-accept the required_stratt pin-bump prompt")
	cmd.Flags().BoolVar(&skipPinBump, "no-pin-bump", false, "skip the required_stratt pin-bump prompt entirely")
	return cmd
}

// maybeBumpRequiredStratt asks (or assumes) whether to set
// `required_stratt` to the current binary's version after a migration,
// per R2.3.13.  No-ops for dev builds and for repos without a project
// config (nowhere to write the pin to).
func maybeBumpRequiredStratt(cmd *cobra.Command, cwd string, b BuildInfo, autoYes bool) error {
	if b.Version == "" || b.Version == "dev" {
		return nil
	}
	proj, err := config.Load(cwd)
	if err != nil || proj == nil || proj.Source == "" {
		return nil
	}
	constraint := fmt.Sprintf(">= %s", b.Version)
	if proj.RequiredStratt == constraint {
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out,
		"\nBump required_stratt to %q in %s? [Y/n] ", constraint, proj.Source)

	run := autoYes
	if !run {
		r := bufio.NewReader(cmd.InOrStdin())
		line, err := r.ReadString('\n')
		if err != nil {
			fmt.Fprintln(out, "(no input; skipping)")
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "", "y", "yes":
			run = true
		}
	}
	if !run {
		fmt.Fprintln(out, "Skipping pin bump.")
		return nil
	}
	if err := config.SetRequiredStratt(proj.Source, constraint); err != nil {
		return fmt.Errorf("setting required_stratt: %w", err)
	}
	st := styleFrom(cmd.Context())
	fmt.Fprintf(out, "%s Set required_stratt = %q in %s\n", st.Green("✓"), constraint, proj.Source)
	return nil
}

func newConfigMigrateBumpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-bump",
		Short: "Consolidate legacy bump-my-version config into native [bump]/[tool.stratt.bump]",
		Long: `Move existing [tool.bumpversion] config (in pyproject.toml or
.bumpversion.toml) into stratt's native location:

  - If stratt.toml exists → [bump] in stratt.toml
  - Else pyproject.toml   → [tool.stratt.bump]
  - Else                  → create stratt.toml with [bump]

The legacy source is left in place; review the migrated file and remove
the old section manually when ready.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			target, source, err := config.MigrateBump(cwd, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"\nMigration complete: %s → %s.\nReview, then remove the old section.\n", source, target)
			return nil
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the resolved project configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			proj, err := config.Load(cwd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			st := styleFrom(cmd.Context())
			if proj.Source == "" {
				fmt.Fprintln(out, "no stratt project config in this repo")
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", st.Bold("Source:          "), proj.Source)
			fmt.Fprintf(out, "%s %s\n", st.Bold("required_stratt: "), emptyDash(proj.RequiredStratt))
			fmt.Fprintf(out, "%s %d\n", st.Bold("Tasks:           "), len(proj.Tasks))
			fmt.Fprintf(out, "%s %d\n", st.Bold("Helpers:         "), len(proj.Helpers))
			if proj.Bump != nil {
				fmt.Fprintf(out, "%s current_version=%s, files=%d\n",
					st.Bold("[bump]:          "), proj.Bump.CurrentVersion, len(proj.Bump.Files))
			}
			return nil
		},
	}
}

func newConfigRequireVersionCmd(b BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "require-version",
		Short: "Write `required_stratt = \">= <current>\"` into project config",
		Long: `Pin the current binary's version as the minimum required by this repo.
Future runs with older stratt will refuse to operate until upgraded.
See requirements R2.3.12 / R2.3.13.

Writes to the existing project config file (stratt.toml or
[tool.stratt] in pyproject.toml).  Errors if no project config exists yet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			proj, err := config.Load(cwd)
			if err != nil {
				return err
			}
			if proj.Source == "" {
				return fmt.Errorf("no project config to write into; create stratt.toml or add [tool.stratt] to pyproject.toml first")
			}
			constraint := fmt.Sprintf(">= %s", b.Version)
			if b.Version == "dev" || b.Version == "" {
				return fmt.Errorf("refusing to pin to a dev/unknown version (%q)", b.Version)
			}
			if err := config.SetRequiredStratt(proj.Source, constraint); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set required_stratt = %q in %s\n", constraint, proj.Source)
			return nil
		},
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
