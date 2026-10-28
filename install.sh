#!/bin/sh
# Installs or updates OpenVPN Desktop from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/mrmmg/openvpn-desktop/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- v0.2.0     # a specific version
#
# Linux: installs the .deb or .rpm that matches your system (the .tar.gz into
# ~/.local/bin otherwise). macOS: installs the app into /Applications.
# Set DRY_RUN=1 to only print what would be done.
set -eu

REPO="mrmmg/openvpn-desktop"
APP="openvpn-desktop"
DRY_RUN="${DRY_RUN:-0}"

say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
run() { if [ "$DRY_RUN" = 1 ]; then printf '[dry-run] %s\n' "$*"; else "$@"; fi; }
have() { command -v "$1" >/dev/null 2>&1; }

if have curl; then
  fetch() { curl -fsSL "$1"; }
  download() { curl -fL --progress-bar -o "$2" "$1"; }
elif have wget; then
  fetch() { wget -qO- "$1"; }
  download() { wget -q --show-progress -O "$2" "$1"; }
else
  die "curl or wget is required"
fi

# Run a command as root, via sudo when needed.
as_root() {
  if [ "$(id -u)" = 0 ]; then run "$@"
  elif have sudo; then run sudo "$@"
  else die "root privileges are required (install sudo or run as root)"
  fi
}

tag_of() { sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1; }

resolve_version() {
  if [ -n "${1:-}" ]; then
    case "$1" in v*) echo "$1" ;; *) echo "v$1" ;; esac
    return
  fi
  tag=$(fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | tag_of || true)
  if [ -z "$tag" ]; then
    # No stable release yet: fall back to the newest one, pre-releases included.
    tag=$(fetch "https://api.github.com/repos/$REPO/releases?per_page=1" 2>/dev/null | tag_of || true)
  fi
  [ -n "$tag" ] || die "could not find a release for $REPO"
  echo "$tag"
}

sha256_of() {
  if have sha256sum; then sha256sum "$1" | cut -d' ' -f1
  elif have shasum; then shasum -a 256 "$1" | cut -d' ' -f1
  else echo ""
  fi
}

TAG=$(resolve_version "${1:-}")
VER=${TAG#v}
BASE="https://github.com/$REPO/releases/download/$TAG"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

say "OpenVPN Desktop $VER"

case "$(uname -s)" in
  Linux)
    case "$(uname -m)" in
      x86_64|amd64) ;;
      *) die "no Linux build for $(uname -m) yet (only x86_64)" ;;
    esac
    prefix="${APP}_${VER}_linux_amd64"
    if have dpkg && have apt-get; then asset="$prefix.deb"
    elif have rpm && { have dnf || have yum || have zypper; }; then asset="$prefix.rpm"
    else asset="$prefix.tar.gz"
    fi
    ;;
  Darwin)
    asset="${APP}_${VER}_macos_universal.zip"
    ;;
  *) die "unsupported system: $(uname -s) (Linux and macOS only)" ;;
esac

say "Downloading $asset"
if [ "$DRY_RUN" = 1 ]; then
  printf '[dry-run] download %s/%s\n' "$BASE" "$asset"
else
  download "$BASE/$asset" "$TMP/$asset" || die "download failed: $BASE/$asset"
  if fetch "$BASE/SHA256SUMS.txt" > "$TMP/SHA256SUMS.txt" 2>/dev/null; then
    want=$(grep " $asset\$" "$TMP/SHA256SUMS.txt" | cut -d' ' -f1)
    got=$(sha256_of "$TMP/$asset")
    if [ -n "$got" ] && [ "$want" != "$got" ]; then die "checksum mismatch for $asset"; fi
    [ -n "$got" ] && say "Checksum OK"
  fi
fi

case "$asset" in
  *.deb)
    say "Installing with apt"
    as_root apt-get install -y "$TMP/$asset"
    ;;
  *.rpm)
    say "Installing with the system package manager"
    if have dnf; then as_root dnf install -y "$TMP/$asset"
    elif have zypper; then as_root zypper --non-interactive --no-gpg-checks install "$TMP/$asset"
    else as_root yum install -y "$TMP/$asset"
    fi
    ;;
  *.tar.gz)
    dest="$HOME/.local/bin"
    share="${XDG_DATA_HOME:-$HOME/.local/share}"
    say "Installing into $dest"
    if [ "$DRY_RUN" != 1 ]; then tar -xzf "$TMP/$asset" -C "$TMP"; fi
    run mkdir -p "$dest" "$share/applications" "$share/icons/hicolor/256x256/apps"
    run install -m 755 "$TMP/$APP" "$dest/$APP"
    run install -m 644 "$TMP/$APP.png" "$share/icons/hicolor/256x256/apps/$APP.png"
    # The menu entry needs the full path, since ~/.local/bin is often not on its PATH.
    if [ "$DRY_RUN" != 1 ]; then
      sed "s|^Exec=.*|Exec=$dest/$APP|" "$TMP/$APP.desktop" > "$share/applications/$APP.desktop"
      have update-desktop-database && update-desktop-database "$share/applications" >/dev/null 2>&1 || true
      have gtk-update-icon-cache && gtk-update-icon-cache -q -t "$share/icons/hicolor" >/dev/null 2>&1 || true
    fi
    say "Needs GTK 4 and WebKitGTK 6 (Arch: pacman -S gtk4 webkitgtk-6.0). Make sure $dest is in your PATH."
    ;;
  *.zip)
    dest="/Applications"
    say "Installing into $dest"
    if [ "$DRY_RUN" != 1 ]; then unzip -q "$TMP/$asset" -d "$TMP/app"; fi
    if [ -w "$dest" ]; then
      run rm -rf "$dest/$APP.app"
      run cp -R "$TMP/app/$APP.app" "$dest/"
      run xattr -cr "$dest/$APP.app"
    else
      as_root rm -rf "$dest/$APP.app"
      as_root cp -R "$TMP/app/$APP.app" "$dest/"
      as_root xattr -cr "$dest/$APP.app"
    fi
    ;;
esac

have openvpn || say "Note: OpenVPN itself is not installed (Linux: your package manager; macOS: brew install openvpn)."
say "Done. Start it from your applications menu or run: $APP"
