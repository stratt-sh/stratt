package cli

import (
	"github.com/spf13/cobra"
)

// newResetCmd implements `stratt reset`: the `clean` verb followed by the
// `setup` verb — one command to wipe and rebuild a repo's dev environment.
//
// Like `all` and `style`, reset is a composite built-in: the resolver
// synthesizes a registry task whose members are the same clean and setup
// tasks the standalone commands run, so project-config customizations of
// either stage apply here too.  This command exists (rather than a plain
// universalSpec entry) only to carry clean's --docker flag.
func newResetCmd() *cobra.Command {
	var docker bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Wipe and rebuild the dev environment (clean + setup)",
		Long: `Run the ` + "`clean`" + ` verb, then the ` + "`setup`" + ` verb, stopping on the first
failure — one command to rebuild a repo's dev environment from scratch.
The canonical use: a python+uv repo whose .venv holds stale interpreter
paths after the repo was moved on disk.

The two stages are exactly the tasks the standalone commands run:
[tasks.clean] / [tasks.setup] overrides and augments apply, and in a
monorepo the setup stage fans out across subprojects just as
` + "`stratt setup`" + ` does.  Pass --docker to forward clean's dangling-image
prune step.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryTaskWithClean(cmd, "reset", docker)
		},
	}
	cmd.Flags().BoolVar(&docker, "docker", false, "also prune dangling docker images during the clean stage (repos with a docker stack)")
	return cmd
}
