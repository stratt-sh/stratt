#!/bin/sh
# stratt installer — downloads a release archive from GitHub, extracts
# the `stratt` binary, and places it in an install directory.
#
# Usage:
#   curl -fsSL https://stratt.sh/install.sh | sh
#   curl -fsSL https://stratt.sh/install.sh | sh -s -- --version v1.14.1
#   curl -fsSL https://stratt.sh/install.sh | sh -s -- --dir /usr/local/bin
#   curl -fsSL https://stratt.sh/install.sh | sh -s -- --keep-brew
#
# Env:
#   STRATT_VERSION   pinned version tag (e.g. v1.14.1).  Defaults to the
#                    latest release.
#   STRATT_INSTALL_DIR
#                    install directory.  Defaults to $HOME/.local/bin
#                    (or /usr/local/bin when running as root).
#   STRATT_REPO      override the upstream repo (default stratt-sh/stratt).
#   STRATT_KEEP_BREW
#                    set to 1 to keep an existing Homebrew-managed stratt
#                    (same as --keep-brew).  By default the script removes
#                    it after a successful install: a brew copy usually
#                    shadows this install on $PATH, and Homebrew 6
#                    silently skips untrusted taps on upgrade.
#
# Verifies the SHA256 checksum against the release's checksums.txt.
# If the `gh` CLI is available, additionally verifies the Sigstore
# artifact attestation against the upstream repo — this is an
# *independent* check (the binary doesn't verify itself).  Set
# STRATT_SKIP_ATTESTATION=1 to skip; --require-attestation forces it.

set -eu

REPO="${STRATT_REPO:-stratt-sh/stratt}"
VERSION="${STRATT_VERSION:-}"
INSTALL_DIR="${STRATT_INSTALL_DIR:-}"
SKIP_ATTESTATION="${STRATT_SKIP_ATTESTATION:-}"
REQUIRE_ATTESTATION=
KEEP_BREW="${STRATT_KEEP_BREW:-}"

while [ $# -gt 0 ]; do
    case "$1" in
        --version)  VERSION="$2"; shift 2 ;;
        --version=*) VERSION="${1#*=}"; shift ;;
        --dir)      INSTALL_DIR="$2"; shift 2 ;;
        --dir=*)    INSTALL_DIR="${1#*=}"; shift ;;
        --repo)     REPO="$2"; shift 2 ;;
        --repo=*)   REPO="${1#*=}"; shift ;;
        --skip-attestation) SKIP_ATTESTATION=1; shift ;;
        --require-attestation) REQUIRE_ATTESTATION=1; shift ;;
        --keep-brew) KEEP_BREW=1; shift ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [ -z "$INSTALL_DIR" ]; then
    if [ "$(id -u)" = "0" ]; then
        INSTALL_DIR=/usr/local/bin
    else
        INSTALL_DIR="${HOME}/.local/bin"
    fi
fi

uname_s=$(uname -s)
uname_m=$(uname -m)
case "$uname_s" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *) echo "unsupported OS: $uname_s" >&2; exit 1 ;;
esac
case "$uname_m" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "unsupported architecture: $uname_m" >&2; exit 1 ;;
esac

# Resolve a version selector to a concrete tag.  Accepted forms:
#   (empty)   → latest stable release
#   vN        → latest release within major N (e.g. v1 → latest v1.x.y)
#   vN.M      → latest release within minor N.M (e.g. v1.14 → latest v1.14.x)
#   vN.M.P    → exact pin
#
# The major/minor forms let setup-stratt@vN pin to a compatible stratt
# major automatically without baking the exact tag into the action.
resolve_version() {
    selector="$1"
    case "$selector" in
        "")
            curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
                | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' \
                | head -n1
            return
            ;;
    esac
    # If the selector already looks fully qualified (has two dots),
    # don't query the API — just normalize prefix.
    case "$selector" in
        v*.*.*|[0-9]*.*.*)
            case "$selector" in
                v*) echo "$selector" ;;
                *)  echo "v${selector}" ;;
            esac
            return
            ;;
    esac
    # Strip leading v for comparison.
    sel_noprefix="${selector#v}"
    # List all release tags (up to 100; bump --paginate if we ever
    # ship more) and pick the highest matching prefix.
    curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=100" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' \
        | grep -E "^v?${sel_noprefix}\.[0-9]+(\.[0-9]+)?$" \
        | sed 's/^v//' \
        | sort -t. -k1,1n -k2,2n -k3,3n \
        | tail -n1 \
        | sed 's/^/v/'
}

if [ -z "$VERSION" ] || expr "$VERSION" : '^v\{0,1\}[0-9][0-9]*$' >/dev/null \
        || expr "$VERSION" : '^v\{0,1\}[0-9][0-9]*\.[0-9][0-9]*$' >/dev/null; then
    resolved=$(resolve_version "$VERSION")
    if [ -z "$resolved" ]; then
        echo "no release matches selector ${VERSION:-latest} in ${REPO}" >&2
        exit 1
    fi
    VERSION="$resolved"
fi
case "$VERSION" in
    v*) version_noprefix="${VERSION#v}" ;;
    *)  version_noprefix="$VERSION"; VERSION="v${VERSION}" ;;
esac

archive="stratt_${version_noprefix}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${VERSION}"
echo "→ stratt ${VERSION} (${os}/${arch}) → ${INSTALL_DIR}"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t stratt-install)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "${base_url}/${archive}" -o "${tmp}/${archive}"
curl -fsSL "${base_url}/checksums.txt" -o "${tmp}/checksums.txt"

# Verify checksum — use the line for our archive only, so unrelated
# missing assets don't fail the check.
expected=$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')
if [ -z "$expected" ]; then
    echo "no checksum found for ${archive} in checksums.txt" >&2
    exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmp}/${archive}" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')
else
    echo "neither sha256sum nor shasum found; cannot verify checksum" >&2
    exit 1
fi
if [ "$expected" != "$actual" ]; then
    echo "checksum mismatch:" >&2
    echo "  expected: ${expected}" >&2
    echo "  actual:   ${actual}" >&2
    exit 1
fi
echo "  ✓ checksum verified"

# Optional provenance check: confirms the archive was built by this repo's
# CI, on top of the checksum + HTTPS checks above. GitHub stores these
# attestations, so `gh` is the verifier. --require-attestation makes a
# missing verifier fatal; STRATT_SKIP_ATTESTATION skips this step.
if [ -z "$SKIP_ATTESTATION" ]; then
    if command -v gh >/dev/null 2>&1; then
        echo "→ verifying provenance via gh"
        if ! gh attestation verify "${tmp}/${archive}" --repo "${REPO}" >/dev/null 2>"${tmp}/gh-attestation.err"; then
            cat "${tmp}/gh-attestation.err" >&2
            echo "provenance verification failed" >&2
            exit 1
        fi
        echo "  ✓ provenance verified"
    elif [ -n "$REQUIRE_ATTESTATION" ]; then
        echo "--require-attestation set but gh is not on PATH" >&2
        exit 1
    else
        echo "  provenance check skipped (install gh to enable)"
    fi
fi

tar -xzf "${tmp}/${archive}" -C "${tmp}" stratt
mkdir -p "${INSTALL_DIR}"
install -m 0755 "${tmp}/stratt" "${INSTALL_DIR}/stratt"

echo "✓ installed ${INSTALL_DIR}/stratt"
case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *) echo "  note: ${INSTALL_DIR} is not on \$PATH" ;;
esac

# Remove a Homebrew-managed install, if one exists.  Running this script
# is an explicit choice of the direct-install path, and a leftover brew
# cask keeps a stale stratt on $PATH — usually AHEAD of the install dir,
# shadowing the copy just installed.  Homebrew 6 also silently skips
# untrusted taps during `brew upgrade`, so the brew copy never updates.
# --keep-brew opts out.  Detection is filesystem-based (Caskroom dir): on
# machines with an untrusted tap, `brew list --cask stratt` refuses to
# load the cask and would miss exactly the installs that most need
# migrating.
brew_caskroom=
for prefix in /opt/homebrew /usr/local /home/linuxbrew/.linuxbrew; do
    if [ -d "${prefix}/Caskroom/stratt" ]; then
        brew_caskroom="${prefix}/Caskroom/stratt"
        break
    fi
done
if [ -z "$brew_caskroom" ] && command -v brew >/dev/null 2>&1; then
    prefix=$(brew --prefix 2>/dev/null || true)
    if [ -n "$prefix" ] && [ -d "${prefix}/Caskroom/stratt" ]; then
        brew_caskroom="${prefix}/Caskroom/stratt"
    fi
fi

if [ -n "$brew_caskroom" ]; then
    if [ -n "$KEEP_BREW" ]; then
        echo "  note: keeping the Homebrew-managed stratt at ${brew_caskroom} (--keep-brew);"
        echo "        it may shadow ${INSTALL_DIR}/stratt depending on \$PATH order."
    else
        echo "→ removing Homebrew-managed stratt (pass --keep-brew to keep it)"
        if brew uninstall --cask stratt; then
            echo "  ✓ removed (the tap remains; drop it with: brew untap stratt-sh/tap)"
        else
            echo "  brew uninstall failed — the old copy may shadow ${INSTALL_DIR}/stratt." >&2
            echo "  Remove it manually with:" >&2
            echo "    brew trust stratt-sh/tap && brew uninstall --cask stratt" >&2
        fi
    fi
fi
