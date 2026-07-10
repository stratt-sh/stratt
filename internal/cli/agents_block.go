package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// The managed block is a stable *pointer*, not a copy of the command
// map: it tells any agent that stratt is in use and where to learn it
// (`stratt agents context`).  Because it carries no repo-specific detail,
// it rarely changes — `sync` only rewrites it if this boilerplate is
// revised, while the live command map is always fetched fresh by
// `agents context`.
//
// Markers are CommonMark HTML-comment blocks: valid Markdown, omitted
// from rendered output, and (in CLAUDE.md) stripped before the model
// sees them — so they cost nothing yet let stratt find its block again.
const (
	agentsBeginMarker = "<!-- BEGIN stratt (managed — run `stratt agents sync`, do not edit) -->"
	agentsEndMarker   = "<!-- END stratt -->"

	agentsBlockBody = "This project uses **stratt** as its task runner. Always prefer stratt\n" +
		"verbs over the underlying tools — `stratt test`, not `go test`/`pytest`;\n" +
		"`stratt build`, not `make`/`cargo build` — stratt detects the toolchain\n" +
		"and dispatches, so behavior matches CI and every other repo here.\n" +
		"\n" +
		"Verbs: `build` · `test` · `lint` · `format` · `style` · `all` (full\n" +
		"verification suite) · `run <task>` (custom tasks) · `release patch|minor|major`.\n" +
		"Non-interactive: `--ci` (no prompts, fail loudly) and `-y` (auto-confirm).\n" +
		"\n" +
		"Before your first stratt command, run `stratt agents context` — it prints\n" +
		"this repo's resolved command map and conventions. Docs: https://stratt.sh"
)

// managedBlock returns the full block, markers included.
func managedBlock() string {
	return agentsBeginMarker + "\n" + agentsBlockBody + "\n" + agentsEndMarker
}

// hasManagedBlock reports whether content already contains a stratt block.
func hasManagedBlock(content string) bool {
	bi := strings.Index(content, agentsBeginMarker)
	ei := strings.Index(content, agentsEndMarker)
	return bi >= 0 && ei > bi
}

// upsertManagedBlock returns content with the stratt block present and
// current, plus whether a block already existed.  When a block is found
// it is replaced in place (markers included), leaving every other byte
// untouched; otherwise the block is appended after a blank-line gap.
func upsertManagedBlock(content string) (updated string, existed bool) {
	block := managedBlock()
	bi := strings.Index(content, agentsBeginMarker)
	ei := strings.Index(content, agentsEndMarker)
	if bi >= 0 && ei > bi {
		end := ei + len(agentsEndMarker)
		return content[:bi] + block + content[end:], true
	}
	if content == "" {
		return block + "\n", false
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + "\n" + block + "\n", false
}

// newAgentsInitCmd adds the stratt pointer block to AGENTS.md, creating
// the file if it does not exist.  Idempotent: if a block is already
// present it is a no-op unless --force is given.
func newAgentsInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Add a stratt pointer block to AGENTS.md (creates it if absent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			path := agentsFilePath(cwd)
			out := cmd.OutOrStdout()

			data, readErr := os.ReadFile(path)
			if readErr != nil && !os.IsNotExist(readErr) {
				return readErr
			}
			content := string(data)
			created := os.IsNotExist(readErr)

			if hasManagedBlock(content) && !force {
				fmt.Fprintln(out, "AGENTS.md already has a stratt block — use `stratt agents sync` to refresh, or --force to rewrite.")
				return nil
			}

			updated, _ := upsertManagedBlock(content)
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return err
			}
			if created {
				fmt.Fprintf(out, "created AGENTS.md with stratt block\n")
			} else {
				fmt.Fprintf(out, "added stratt block to AGENTS.md\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rewrite the stratt block even if one already exists")
	return cmd
}

// newAgentsSyncCmd regenerates the stratt block in AGENTS.md from the
// current boilerplate.  Errors (pointing to `init`) if no block exists.
func newAgentsSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Refresh the stratt block in AGENTS.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			path := agentsFilePath(cwd)
			out := cmd.OutOrStdout()

			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				return fmt.Errorf("no AGENTS.md found — run `stratt agents init` first")
			}
			if err != nil {
				return err
			}
			content := string(data)
			if !hasManagedBlock(content) {
				return fmt.Errorf("AGENTS.md has no stratt block — run `stratt agents init` first")
			}

			updated, _ := upsertManagedBlock(content)
			if updated == content {
				fmt.Fprintln(out, "stratt block already up to date")
				return nil
			}
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(out, "refreshed stratt block in AGENTS.md")
			return nil
		},
	}
}
