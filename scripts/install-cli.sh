#!/usr/bin/env bash
# Multica CLI installer (Lilith build).
#
# Installs / upgrades the `multica` CLI straight from Lilith's download
# host — the same origin the Desktop app downloads from
# (https://multica.lilithgames.com/api/downloads). It is published to OSS
# on every release as `install.sh`, so users install with:
#
#   curl -fsSL https://multica.lilithgames.com/api/downloads/install.sh | bash
#
# Lilith publishes the CLI for Linux amd64 and arm64 only. On any other
# platform this script points the user at the Desktop download page (which
# bundles the CLI). After install, run `multica setup` to configure.
set -euo pipefail

# Download origin. Overridable for testing against a staging host.
BASE_URL="${MULTICA_DOWNLOAD_BASE:-https://multica.lilithgames.com/api/downloads}"
BASE_URL="${BASE_URL%/}"
DOWNLOAD_PAGE="https://multica.lilithgames.com/download"

# Colors (disabled when not a terminal)
if [ -t 1 ] || [ -t 2 ]; then
  BOLD='\033[1m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'
  RED='\033[0;31m'; CYAN='\033[0;36m'; RESET='\033[0m'
else
  BOLD='' GREEN='' YELLOW='' RED='' CYAN='' RESET=''
fi

info()  { printf "${BOLD}${CYAN}==> %s${RESET}\n" "$*"; }
ok()    { printf "${BOLD}${GREEN}✓ %s${RESET}\n" "$*"; }
warn()  { printf "${BOLD}${YELLOW}⚠ %s${RESET}\n" "$*" >&2; }
fail()  { printf "${BOLD}${RED}✗ %s${RESET}\n" "$*" >&2; exit 1; }

command_exists() { command -v "$1" >/dev/null 2>&1; }

detect_platform() {
  local os arch
  os="$(uname -s)"
  case "$os" in
    Linux) ;;
    Darwin|MINGW*|MSYS*|CYGWIN*)
      fail "Lilith publishes the CLI for Linux only. On macOS/Windows the CLI ships inside the Desktop app — download it from:
  ${DOWNLOAD_PAGE}" ;;
    *)
      fail "Unsupported operating system: ${os}. The Lilith CLI supports Linux (amd64, arm64)." ;;
  esac
  OS="linux"

  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fail "Unsupported architecture: ${arch}. The Lilith CLI supports amd64 and arm64." ;;
  esac
}

# Reads the current release version from the CLI version pointer. This is
# published by the same release job that uploads the tarballs, so the
# pointer and the tarball it names are always consistent.
latest_version() {
  local body
  body="$(curl -fsSL "${BASE_URL}/latest-cli.txt" 2>/dev/null | head -n 1 | tr -d ' \r\n' || true)"
  printf '%s' "${body#v}"
}

installed_version() {
  # `multica version` prints e.g. "multica 0.3.23 (commit: …)"; grab field 2.
  multica version 2>/dev/null | awk 'NR==1{print $2}' | sed 's/^v//' || true
}

add_to_path() {
  local dir="$1"
  local line="export PATH=\"${dir}:\$PATH\""
  for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
    if [ -f "$rc" ] && ! grep -qF "$dir" "$rc"; then
      printf '\n# Added by Multica installer\n%s\n' "$line" >> "$rc"
    fi
  done
}

install_binary() {
  local version="$1"
  local url="${BASE_URL}/multica-cli-${version}-${OS}-${ARCH}.tar.gz"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_dir'" RETURN

  info "Downloading ${url} ..."
  if ! curl -fsSL "$url" -o "$tmp_dir/multica.tar.gz"; then
    fail "Failed to download the CLI binary. Check your network connection, or grab it from ${DOWNLOAD_PAGE}."
  fi
  tar -xzf "$tmp_dir/multica.tar.gz" -C "$tmp_dir" multica

  # Prefer /usr/local/bin; fall back to ~/.local/bin. MULTICA_BIN_DIR
  # overrides the first choice (used by tests / scripted installs).
  local bin_dir="${MULTICA_BIN_DIR:-/usr/local/bin}"
  if [ -w "$bin_dir" ]; then
    mv "$tmp_dir/multica" "$bin_dir/multica"
  elif command_exists sudo; then
    sudo mv "$tmp_dir/multica" "$bin_dir/multica"
  else
    bin_dir="$HOME/.local/bin"
    mkdir -p "$bin_dir"
    mv "$tmp_dir/multica" "$bin_dir/multica"
    if ! printf '%s' "$PATH" | tr ':' '\n' | grep -qx "$bin_dir"; then
      export PATH="$bin_dir:$PATH"
      add_to_path "$bin_dir"
    fi
  fi
  chmod +x "$bin_dir/multica"
  ok "Multica CLI installed to ${bin_dir}/multica"
}

main() {
  printf "\n${BOLD}  Multica — CLI Installer${RESET}\n\n"

  detect_platform

  local latest
  latest="$(latest_version)"
  [ -n "$latest" ] || fail "Could not determine the latest CLI version from ${BASE_URL}/latest-cli.txt. Check your network connection."

  if command_exists multica; then
    local current
    current="$(installed_version)"
    if [ -n "$current" ] && [ "$current" = "$latest" ]; then
      ok "Multica CLI is up to date (${current})"
      printf "\n  Run ${CYAN}multica setup${RESET} to configure your environment.\n\n"
      return 0
    fi
    info "Multica CLI ${current:-unknown} installed, latest is ${latest} — upgrading..."
    install_binary "$latest"
  else
    install_binary "$latest"
  fi

  if ! command_exists multica; then
    fail "CLI installed but 'multica' is not on PATH. Restart your shell and try 'multica version'."
  fi

  printf "\n${BOLD}${GREEN}  ✓ Multica CLI is ready!${RESET}\n\n"
  printf "  ${BOLD}Next:${RESET} configure your environment\n\n"
  printf "     ${CYAN}multica setup${RESET}\n\n"
}

main "$@"
