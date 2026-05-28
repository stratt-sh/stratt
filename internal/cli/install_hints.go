package cli

// InstallHint returns a one-line install command for tool, or "" when
// stratt has no specific suggestion to offer.  Used by `stratt doctor`
// to surface actionable next steps when a resolved engine's binary
// isn't on `$PATH`.
//
// Defaults lean macOS/Homebrew.  Where brew isn't appropriate (Python
// tools), suggest `uv tool install` since uv is already the assumed
// Python toolchain for stratt-aware repos.
//
// New entries are welcome — keep the value short enough that the
// `tool → suggestion` table stays readable on an 80-col terminal.
func InstallHint(tool string) string {
	switch tool {
	// Static-site generators
	case "hugo":
		return "brew install hugo"
	case "mkdocs":
		return "uv tool install mkdocs-material"
	case "sphinx-build":
		return "uv tool install sphinx"
	case "sphinx-autobuild":
		return "uv tool install sphinx-autobuild"

	// Python toolchain
	case "uv":
		return "brew install uv"
	case "ruff":
		return "uv tool install ruff"
	case "pytest":
		return "uv tool install pytest"
	case "bump-my-version":
		return "uv tool install bump-my-version"

	// Go toolchain
	case "go":
		return "brew install go"
	case "gofmt":
		return "comes with Go — `brew install go`"
	case "golangci-lint":
		return "brew install golangci-lint"
	case "goreleaser":
		return "brew install goreleaser"

	// PHP / containers / orchestration
	case "composer":
		return "brew install composer"
	case "docker":
		return "install Docker Desktop, or `brew install --cask docker`"
	case "kubectl":
		return "brew install kubectl"

	// Git / system
	case "git":
		return "preinstalled on most systems; otherwise `brew install git`"

	// GitHub Actions
	case "actionlint":
		return "brew install actionlint"

	// Ansible
	case "ansible-lint":
		return "uv tool install ansible-lint"
	case "ansible-galaxy", "ansible-playbook", "ansible-test":
		return "uv tool install ansible-core"
	}
	return ""
}
