package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/stratt-sh/stratt/internal/config"
	"github.com/stratt-sh/stratt/internal/git"
	"github.com/stratt-sh/stratt/internal/kustomize"
	yaml "gopkg.in/yaml.v3"
)

// newDeployCmd implements `stratt deploy <env> <version>`.  Default
// flow: check clean tree → edit kustomization.yaml → commit → push.
// `--no-commit` and `--no-push` cover the partial paths.
func newDeployCmd() *cobra.Command {
	var (
		imageName  string
		noCommit   bool
		noPush     bool
		yes        bool
		remoteFlag string
		branchFlag string
	)
	cmd := &cobra.Command{
		Use:   "deploy <env> <version>",
		Short: "Bump Kustomize image tags and ship the deploy",
		Long: `Update the image tag in deploy/overlays/<env>/kustomization.yaml
to <version> (preserving comments and formatting), then commit and push.

Examples:
  stratt deploy prod 1.14.1
  stratt deploy staging 1.15.0-rc1 --no-push           # commit locally, push later
  stratt deploy prod 1.14.1 --no-commit                # edit only, no git activity
  stratt deploy prod 1.14.1 --image=myapp              # disambiguate multi-image overlay
  stratt deploy envs                                   # list available environments`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, version := args[0], args[1]
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			proj, _ := config.Load(cwd)
			usr, _ := config.LoadUser()
			doCommit, doPush := resolveDeployCommitPush(cmd, noCommit, noPush, proj, usr)

			effectiveImage := imageName
			if effectiveImage == "" && proj != nil && proj.Deploy != nil {
				effectiveImage = proj.Deploy.PrimaryImage
			}

			return runDeploy(cmd.Context(), deployRequest{
				cwd:       cwd,
				env:       env,
				version:   version,
				image:     effectiveImage,
				commit:    doCommit,
				push:      doPush,
				assumeYes: yes,
				remote:    remoteFlag,
				branch:    branchFlag,
				stdout:    cmd.OutOrStdout(),
				stdin:     cmd.InOrStdin(),
			})
		},
	}
	cmd.Flags().StringVar(&imageName, "image", "", "specific image name to update (required if the overlay has multiple images)")
	cmd.Flags().BoolVar(&noCommit, "no-commit", false, "edit the kustomization but do not commit or push")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "commit locally but do not push (default is to push)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the commit confirmation prompt")
	cmd.Flags().StringVar(&remoteFlag, "remote", "", "git remote to push to (default: origin)")
	cmd.Flags().StringVar(&branchFlag, "branch", "", "branch to push (default: the current branch)")

	cmd.AddCommand(newDeployEnvsCmd())
	return cmd
}

// resolveDeployCommitPush layers CLI > project > user > default for the
// commit/push booleans.  Built-in default is true/true (deploy = ship).
//
// CLI `--no-commit` / `--no-push` are explicit overrides and always win.
// Project config wins next, user config last.
func resolveDeployCommitPush(cmd *cobra.Command, noCommitFlag, noPushFlag bool, proj *config.Project, usr *config.User) (commit, push bool) {
	commit, push = true, true

	// User layer (lowest priority among config sources).
	if usr != nil && usr.Deploy != nil {
		if usr.Deploy.Commit != nil {
			commit = *usr.Deploy.Commit
		}
		if usr.Deploy.Push != nil {
			push = *usr.Deploy.Push
		}
	}
	// Project layer overrides user.
	if proj != nil && proj.Deploy != nil {
		if proj.Deploy.Commit != nil {
			commit = *proj.Deploy.Commit
		}
		if proj.Deploy.Push != nil {
			push = *proj.Deploy.Push
		}
	}
	// CLI flags win if explicitly passed.
	if cmd.Flags().Changed("no-commit") {
		commit = !noCommitFlag
	}
	if cmd.Flags().Changed("no-push") {
		push = !noPushFlag
	}
	// Can't push without committing.
	if !commit {
		push = false
	}
	return commit, push
}

// deployRequest carries the fully-resolved inputs for one deploy.  It is
// the shared contract between `stratt deploy` (which fills it from CLI
// flags + config) and `stratt release --deploy <env>` (which chains a
// deploy of the freshly-released version).
type deployRequest struct {
	cwd       string
	env       string
	version   string
	image     string // resolved image name; "" lets kustomize pick the sole image
	commit    bool
	push      bool
	assumeYes bool
	remote    string
	branch    string
	stdout    io.Writer
	stdin     io.Reader
}

// runDeploy edits deploy/overlays/<env>/kustomization.yaml to point at
// version, then (per the request) commits and pushes.  It is the engine
// behind both the `deploy` command and the `release --deploy` follow-up.
func runDeploy(ctx context.Context, req deployRequest) error {
	overlay := kustomize.OverlayPath(req.cwd, req.env)
	if _, err := os.Stat(overlay); err != nil {
		return fmt.Errorf("no overlay at %s (run `stratt deploy envs` to list available environments)", overlay)
	}

	repo := git.New(req.cwd)

	// Clean-tree check protects the deploy commit from picking up
	// unrelated edits.  Skipped when committing is disabled.
	if req.commit {
		clean, err := repo.IsClean(ctx)
		if err != nil {
			return fmt.Errorf("checking working tree: %w", err)
		}
		if !clean {
			return fmt.Errorf("working tree is not clean (commit or stash your changes before deploying, or pass --no-commit to edit only)")
		}
	}

	change, err := kustomize.SetImage(overlay, req.image, req.version)
	if err != nil {
		return err
	}
	fmt.Fprintf(req.stdout,
		"updated %s\n  %s: %s → %s\n",
		relTo(req.cwd, overlay), change.Image, displayTag(change.OldTag), change.NewTag)

	overlayRel := relTo(req.cwd, overlay)
	if !req.commit {
		fmt.Fprintf(req.stdout,
			"\nEdit-only mode (commit disabled).  To commit & push later:\n"+
				"  git add %s\n  git commit -m %q\n  git push origin %s\n",
			overlayRel,
			firstLine(defaultDeployMessage(req.env, overlayRel, change)),
			defaultDeployBranch(req.branch))
		return nil
	}

	if !req.assumeYes {
		if !confirmCommit(req.stdout, req.stdin) {
			fmt.Fprintln(req.stdout,
				"Skipping git; file change remains in working tree.")
			return nil
		}
	}

	if err := repo.Add(ctx, overlay); err != nil {
		return err
	}
	msg := defaultDeployMessage(req.env, overlayRel, change)
	if err := repo.Commit(ctx, msg); err != nil {
		return err
	}
	fmt.Fprintf(req.stdout, "✓ committed: %s\n", firstLine(msg))

	// Push unless explicitly disabled.
	if !req.push {
		fmt.Fprintf(req.stdout,
			"\nNot pushed (push disabled).  When ready:\n  git push %s %s\n",
			orDefault(req.remote, "origin"),
			defaultDeployBranch(req.branch))
		return nil
	}
	branch := req.branch
	if branch == "" {
		detected, err := repo.Branch(ctx)
		if err != nil {
			return fmt.Errorf("detecting current branch: %w", err)
		}
		branch = detected
	}
	remote := orDefault(req.remote, "origin")
	fmt.Fprintf(req.stdout, "→ pushing to %s/%s\n", remote, branch)
	if err := repo.PushBranch(ctx, remote, branch); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	fmt.Fprintf(req.stdout, "✓ pushed to %s/%s\n", remote, branch)
	fmt.Fprintf(req.stdout, "\n✓ Deployed %s %s to %s.\n",
		change.Image, change.NewTag, req.env)
	return nil
}

// defaultDeployMessage returns the commit message stratt uses for an
// image-tag deploy.
//
// Subject:
//
//	stratt deploy: <image> version <new> to <env>
//
// The `stratt deploy:` prefix makes commits made by stratt grep-friendly
// (`git log --grep='^stratt deploy:'`).  The body records the file
// path and the tag transition so reviewers see the actual change at a
// glance from `git log` alone.
func defaultDeployMessage(env, overlay string, change *kustomize.ImageChange) string {
	oldTag := change.OldTag
	if oldTag == "" {
		oldTag = "(unset)"
	}
	return fmt.Sprintf(
		"stratt deploy: %s version %s to %s\n\nUpdated %s:\n  %s: %s → %s",
		change.Image, change.NewTag, env,
		overlay,
		change.Image, oldTag, change.NewTag,
	)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func defaultDeployBranch(b string) string {
	if b == "" {
		return "<current-branch>"
	}
	return b
}

func displayTag(t string) string {
	if t == "" {
		return "(unset)"
	}
	return t
}

func confirmCommit(out io.Writer, in io.Reader) bool {
	fmt.Fprint(out, "Commit and push this change? [Y/n] ")
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// newDeployEnvsCmd implements `stratt deploy envs` — list overlays and
// the image tag(s) currently set in each.  Does not modify anything.
func newDeployEnvsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "envs",
		Short: "List deploy environments and their current image tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			envs, err := listOverlayEnvs(cwd)
			if err != nil {
				return err
			}
			if len(envs) == 0 {
				return fmt.Errorf("no overlays found in %s",
					filepath.Join(cwd, "deploy", "overlays"))
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ENVIRONMENT\tIMAGES")
			for _, e := range envs {
				fmt.Fprintf(tw, "%s\t%s\n", e.Name, formatImageTags(e.Images))
			}
			return tw.Flush()
		},
	}
}

// overlayEnv captures the snapshot of one overlay's kustomization.yaml
// for `stratt deploy envs` display.
type overlayEnv struct {
	Name   string
	Images []overlayImage
}

type overlayImage struct {
	Name   string
	NewTag string
}

// listOverlayEnvs walks deploy/overlays/* and returns each environment
// directory that contains a readable kustomization.yaml, with its
// `images:` entries parsed.
func listOverlayEnvs(root string) ([]overlayEnv, error) {
	overlaysRoot := filepath.Join(root, "deploy", "overlays")
	entries, err := os.ReadDir(overlaysRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []overlayEnv
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		kpath := filepath.Join(overlaysRoot, e.Name(), "kustomization.yaml")
		images, err := readOverlayImages(kpath)
		if err != nil {
			// Skip overlays we can't parse rather than failing the
			// whole listing.  Surface the issue inline via an "(error)"
			// placeholder so the user can still see the entry.
			out = append(out, overlayEnv{
				Name:   e.Name(),
				Images: []overlayImage{{Name: "(error reading kustomization.yaml)"}},
			})
			continue
		}
		out = append(out, overlayEnv{Name: e.Name(), Images: images})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readOverlayImages parses just enough YAML to extract the
// `images: [{name, newTag}, ...]` array from a kustomization.yaml.
// Returns an empty slice (no error) if the file has no `images:` block.
func readOverlayImages(path string) ([]overlayImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Images []struct {
			Name   string `yaml:"name"`
			NewTag string `yaml:"newTag"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]overlayImage, 0, len(doc.Images))
	for _, img := range doc.Images {
		out = append(out, overlayImage{Name: img.Name, NewTag: img.NewTag})
	}
	return out, nil
}

func formatImageTags(images []overlayImage) string {
	if len(images) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(images))
	for _, img := range images {
		tag := img.NewTag
		if tag == "" {
			tag = "(unset)"
		}
		parts = append(parts, fmt.Sprintf("%s:%s", img.Name, tag))
	}
	return strings.Join(parts, ", ")
}
