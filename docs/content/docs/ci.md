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
| `v0`      | Latest `v0.x.y` (compatible major)   | **CI default** — gets fixes, never a breaking major |
| `v0.5`    | Latest `v0.5.x` (compatible minor)   | Conservative pin                                 |
| `v0.5.1`  | Exact pin                            | Fully reproducible builds                        |

```sh
curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v0
curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v0.5.1
```

Other flags: `--dir <path>`, `--repo owner/name` (for forks).

## GitHub Actions

On GitHub Actions, use the [`setup-stratt`](https://github.com/stratt-sh/setup-stratt) action rather than calling the install script directly. It downloads the binary, verifies its checksum **and** its GitHub artifact attestation before anything executes, and adds it to `PATH`:

```yaml
jobs:
  ci:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      attestations: read   # required for attestation verification (see below)
    steps:
      - uses: actions/checkout@v6
      - uses: stratt-sh/setup-stratt@v0
      - run: stratt all
```

`@v0` is a floating tag that fast-forwards on each non-breaking action release: you get fixes for free, never a surprise breaking change. By default the action installs the latest stratt CLI within its own major — `@v0` installs the latest `stratt v0.x.y`.

Pin the stratt CLI version with the `version` input:

```yaml
      - uses: stratt-sh/setup-stratt@v0
        with:
          version: v0.5.1     # exact pin — fully reproducible
          # version: v0.5     # latest v0.5.x
          # version: latest   # latest stable across any major
```

For high-security pipelines, pin the action itself to a commit SHA (tags are mutable) and let Dependabot or Renovate bump it:

```yaml
      - uses: stratt-sh/setup-stratt@<sha>  # v0.3.0
```

### Required permissions

`setup-stratt` verifies the release attestation via `gh attestation verify`, which fetches the bundle from GitHub's attestations API. That call requires the workflow job to grant:

```yaml
permissions:
  attestations: read
```

(`id-token: write` is **not** required — that permission is for *producing* attestations or for OIDC-based external auth. Verification is read-only on GitHub's side; the Sigstore signature check happens locally against the public-good trust root.)

If your org policy forbids granting `attestations: read`, set `require-attestation: false` on the action — the job then soft-skips attestation verification, but checksum verification against `checksums.txt` still runs unconditionally:

```yaml
      - uses: stratt-sh/setup-stratt@v0
        with:
          require-attestation: false
```

### Other CI systems

Outside GitHub Actions (GitLab CI, CircleCI, self-hosted images, …) use the [install script](#install) directly. Pin a major so you get fixes but never a breaking change:

```sh
curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v0
```

The script's own attestation behavior — `--require-attestation` to fail hard, `--skip-attestation` to opt out — is covered under [Attestation verification](#attestation-verification) below.

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
