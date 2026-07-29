package cli

import "github.com/stratt-sh/stratt/internal/capability"

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
		// mkdocs-material is a theme, not the tool — it ships no
		// executables, so `uv tool install mkdocs-material` always fails.
		// Install mkdocs itself, with the theme (and any project plugins,
		// e.g. mkdocstrings) layered in via --with.
		return "uv tool install mkdocs --with mkdocs-material (add --with <plugin> per mkdocs plugin)"
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

// installHintInRepo returns the install suggestion for tool, adjusted
// for repo context.  In a python+uv repo a missing docs tool is better
// fixed by making it a project dependency (docs then resolves through
// `uv run`, no global install at all) than by a PATH install.  For
// sphinx a global install isn't just worse, it's broken: autodoc must
// import the project package and its theme/extensions, which only the
// project venv provides.
func installHintInRepo(r *capability.Resolver, tool string) string {
	if r != nil && r.HasStack("python+uv") {
		switch tool {
		case "mkdocs":
			return "add mkdocs to the project's dev dependencies, then `stratt sync`"
		case "sphinx-build":
			return "add sphinx to the project's dev dependencies, then `stratt sync`"
		case "sphinx-autobuild":
			return "add sphinx-autobuild to the project's dev dependencies, then `stratt sync`"
		}
	}
	return InstallHint(tool)
}
