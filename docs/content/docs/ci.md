---
title: Using stratt in CI
linkTitle: CI / CD
weight: 5
---

Stratt runs unchanged on Linux runners — the same universal commands (`stratt test`, `stratt lint`, `stratt all`, `stratt release`, `stratt deploy`) work in CI as they do locally. The only thing CI needs is a way to *install* the binary.

## Install

The install script handles macOS and Linux (amd64 + arm64). It downloads the matching release archive, verifies the SHA256 against the release's `checksums.txt`, and drops the binary into `~/.local/bin` (or `/usr/local/bin` when run as root).

```sh
curl -fsSL https://stratt.sh/install.sh | sh
```

The `--version` flag accepts three forms:

| Selector  | Resolves to                          | Use case                                         |
|-----------|--------------------------------------|--------------------------------------------------|
| (omitted) | Latest stable release                | Workstations, fast-iterating projects            |
| `v1`      | Latest `v1.x.y` (compatible major)   | **CI default** — gets fixes, never a breaking major |
| `v1.14`   | Latest `v1.14.x` (compatible minor)  | Conservative pin                                 |
| `v1.14.1` | Exact pin                            | Fully reproducible builds                        |

```sh
curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v1
curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v1.14.1
```

Other flags: `--dir <path>`, `--repo owner/name` (for forks).

## GitHub Actions

```yaml
jobs:
  ci:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      attestations: read   # required for `gh attestation verify` (see below)
    steps:
      - uses: actions/checkout@v6
      - name: Install stratt
        run: |
          curl -fsSL https://stratt.sh/install.sh | sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: stratt all
```

For long-lived CI, pin to a major version so you get bug fixes but not breaking changes:

```yaml
      - name: Install stratt
        run: |
          curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v1
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
```

This installs the latest `v1.x.y` release at the time the workflow runs. Stratt follows semver: patch and minor releases within a major are backwards-compatible by policy. When you're ready to upgrade past a breaking change, bump the selector to `v2`.

For fully reproducible CI (lockfile-level), use an exact version:

```yaml
          curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v1.14.1
```

### Required permissions

The install script verifies the release attestation via `gh attestation verify`, which fetches the bundle from GitHub's attestations API. That call requires the workflow job to grant:

```yaml
permissions:
  attestations: read
```

(`id-token: write` is **not** required — that permission is for *producing* attestations or for OIDC-based external auth. Verification is read-only on GitHub's side; the Sigstore signature check happens locally against the public-good trust root.)

If your repo defaults to the GitHub-restricted permission set (or your org pins permissions to read-only), the verification step will fail with a 403 from the attestations endpoint. The install script falls back to soft-skip in that case unless `--require-attestation` is set, in which case the job fails fast — which is what you usually want, since silently dropping attestation verification defeats the point.

If you can't grant `attestations: read` for policy reasons, run the installer with `--skip-attestation` and document the decision in your repo. SHA256 against `checksums.txt` is still verified.

## What stratt skips in CI

When `$CI` or `$GITHUB_ACTIONS` is set, stratt automatically:

- skips the every-invocation "update available" notifier
- refuses `stratt self update` (you should install fresh per run, not mutate the runner)

Use a pinned version + the install script and you'll get deterministic, attestation-backed binaries on every run.

## Attestation verification

Bootstrapping trust in a binary requires an *independent* verifier — asking the freshly-downloaded binary to verify itself is circular (a tampered binary can simply claim to be valid). The install script handles this by calling `gh attestation verify` against the downloaded archive **before** the binary is ever executed.

On GitHub-hosted runners `gh` is pre-installed, so attestation verification happens automatically. The script will:

1. SHA256 the archive against `checksums.txt`
2. Run `gh attestation verify <archive> --repo stratt-sh/stratt`
3. Only extract + install if both pass

To make a missing `gh` a hard failure instead of a soft skip:

```sh
curl -fsSL https://stratt.sh/install.sh | sh -s -- --require-attestation
```

To skip entirely (not recommended outside trusted networks):

```sh
curl -fsSL https://stratt.sh/install.sh | sh -s -- --skip-attestation
# or: STRATT_SKIP_ATTESTATION=1 curl ... | sh
```

`stratt self verify` exists too, but it's for *tamper detection on an already-installed binary*, not for first-install trust. Once you've installed stratt through a verified chain, running `stratt self verify` later can catch on-disk modification, but the verification result is only as trustworthy as the binary running the check.
