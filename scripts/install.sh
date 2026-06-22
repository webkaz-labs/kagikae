#!/usr/bin/env sh
set -eu

# Install the kae binary from a GitHub release, verifying the release checksum
# before copying. Usage:
#   curl -fsSL https://raw.githubusercontent.com/webkaz-labs/kagikae/main/scripts/install.sh | sh
#   ... | sh -s -- --version vX.Y.Z --install-dir ~/.local/bin

repo="${KAE_REPO:-webkaz-labs/kagikae}"
version="${KAE_VERSION:-latest}"
install_dir="${KAE_INSTALL_DIR:-$HOME/.local/bin}"

usage() {
  cat <<'EOF'
Usage: install.sh [--version VERSION] [--install-dir DIR]

Environment:
  KAE_VERSION      Release tag to install. Defaults to latest.
  KAE_INSTALL_DIR  Directory for the kae binary. Defaults to ~/.local/bin.
  KAE_REPO         GitHub repo owner/name. Defaults to webkaz-labs/kagikae.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      if [ "$#" -lt 2 ]; then
        echo "install.sh: --version requires a value" >&2
        exit 2
      fi
      version="$2"
      shift 2
      ;;
    --install-dir)
      if [ "$#" -lt 2 ]; then
        echo "install.sh: --install-dir requires a value" >&2
        exit 2
      fi
      install_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "install.sh: required command not found: $1" >&2
    exit 1
  fi
}

need curl
need sed
need tar
need awk
need uname
need mktemp
need install

if command -v shasum >/dev/null 2>&1; then
  checksum_tool="shasum"
elif command -v sha256sum >/dev/null 2>&1; then
  checksum_tool="sha256sum"
else
  echo "install.sh: required command not found: shasum or sha256sum" >&2
  exit 1
fi

case "$version" in
  latest)
    version=$(
      curl --fail --location --silent --show-error \
        "https://api.github.com/repos/${repo}/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p'
    )
    if [ -z "$version" ]; then
      echo "install.sh: could not resolve latest release for ${repo}" >&2
      exit 1
    fi
    ;;
  v[0-9]*)
    ;;
  *)
    echo "install.sh: release version must be latest or a tag like vX.Y.Z" >&2
    exit 2
    ;;
esac

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "install.sh: unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="amd64" ;;
  *)
    echo "install.sh: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

# GoReleaser names archives by the version without the leading "v"
# (kae_0.9.0_darwin_arm64.tar.gz) with the binary at the archive root.
archive="kae_${version#v}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"
tmp_parent="${TMPDIR:-/tmp}"
tmp_parent="${tmp_parent%/}"
tmpdir=$(mktemp -d "${tmp_parent}/kae-install.XXXXXX")
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

curl --fail --location --silent --show-error \
  --output "${tmpdir}/${archive}" \
  "${base_url}/${archive}"
curl --fail --location --silent --show-error \
  --output "${tmpdir}/checksums.txt" \
  "${base_url}/checksums.txt"

# GoReleaser's checksums.txt usually uses "<sha256>  <archive>". Accept a
# leading "./" too so the installer survives checksum path-format changes.
if ! awk -v target="${archive}" '$2 == target || $2 == "./" target { print; found = 1 } END { exit found ? 0 : 1 }' \
  "${tmpdir}/checksums.txt" > "${tmpdir}/checksum.txt"; then
  echo "install.sh: checksum entry not found for ${archive}" >&2
  exit 1
fi

binary_path="${tmpdir}/kae"

(
  cd "$tmpdir"
  if [ "$checksum_tool" = "shasum" ]; then
    shasum -a 256 -c checksum.txt
  else
    sha256sum -c checksum.txt
  fi
  tar -xzf "$archive" kae
)

if [ ! -f "$binary_path" ] || [ -L "$binary_path" ]; then
  echo "install.sh: extracted binary is not a regular file: kae" >&2
  exit 1
fi

mkdir -p "$install_dir"
install -m 0755 "$binary_path" "${install_dir}/kae"

echo "installed kae ${version} to ${install_dir}/kae"
"${install_dir}/kae" version

# Refresh any already-registered shell completion so a structural change in the
# new version (a new subcommand case / __complete kind) takes effect without a
# manual re-install. Best-effort: refresh only rewrites existing registrations
# and must never fail the install.
"${install_dir}/kae" completion --refresh || true
