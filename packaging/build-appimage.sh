#!/usr/bin/env bash
# Builds the DarkServe AppImage.
#
#   ./packaging/build-appimage.sh                 # x86_64
#   ./packaging/build-appimage.sh --arch aarch64  # arm64
#   ./packaging/build-appimage.sh --arch all     # both
#   ./packaging/build-appimage.sh --no-tools      # skip the bundled ffmpeg/yt-dlp
#
# The result lands in dist/. Everything that is downloaded (the AppImage
# runtime, ffmpeg, yt-dlp) is cached in .appimage-cache, so a rebuild only
# repeats the parts that changed.
set -euo pipefail

ROOT="$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)"
CACHE="$ROOT/.appimage-cache"
DIST="$ROOT/dist"
VERSION="$(cat "$ROOT/VERSION" 2>/dev/null || echo 0.0.0)"

ARCHES=""
BUNDLE_TOOLS=1
COMPRESS="gzip"

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch) ARCHES="${2:-}"; shift 2 ;;
    --arch=*) ARCHES="${1#*=}"; shift ;;
    --no-tools) BUNDLE_TOOLS=0; shift ;;
    --compress) COMPRESS="${2:-}"; shift 2 ;;
    --compress=*) COMPRESS="${1#*=}"; shift ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    -h|--help) usage 0 ;;
    *) echo "unknown option: $1" >&2; usage 1 ;;
  esac
done

[[ -n "$ARCHES" ]] || ARCHES="x86_64"
[[ "$ARCHES" == "all" ]] && ARCHES="x86_64 aarch64"

# Fills GOARCH, FFARCH, YTSUFFIX and RTARCH for the wanted architecture.
arch_info() {
  case "$1" in
    x86_64|amd64)  GOARCH=amd64; FFARCH=amd64; YTSUFFIX="";       RTARCH=x86_64 ;;
    aarch64|arm64) GOARCH=arm64; FFARCH=arm64; YTSUFFIX="_aarch64"; RTARCH=aarch64 ;;
    *) echo "unsupported architecture: $1" >&2; exit 1 ;;
  esac
}

download() {
  # download <url> <destination>
  local url="$1" dest="$2"
  if [[ -s "$dest" ]]; then
    echo "  cached: $(basename "$dest")"
    return 0
  fi
  echo "  fetching $(basename "$dest")"
  if command -v curl >/dev/null 2>&1; then
    curl -fL -sS --retry 3 -o "$dest.part" "$url"
  else
    wget -q -O "$dest.part" "$url"
  fi
  mv "$dest.part" "$dest"
}

mkdir -p "$CACHE" "$DIST"

build_arch() {
  local arch="$1"
  arch_info "$arch"
  local goarch="$GOARCH" ffarch="$FFARCH" ytsuffix="$YTSUFFIX" rtarch="$RTARCH"

  echo
  echo "=== $arch ==="
  local work="$CACHE/work-$arch"
  local appdir="$work/DarkServe.AppDir"
  rm -rf "$appdir"
  mkdir -p "$appdir/usr/bin" "$appdir/usr/share/metainfo" "$appdir/usr/share/icons/hicolor/256x256/apps"

  # --- the server binary -------------------------------------------------
  echo "  building the server"
  ( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
      go build -trimpath -ldflags="-s -w" -o "$appdir/usr/bin/darkserve" ./cmd )

  # --- bundled tools -----------------------------------------------------
  if [[ $BUNDLE_TOOLS -eq 1 ]]; then
    local tarball="$CACHE/ffmpeg-$ffarch.tar.xz"
    download "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${ffarch}-static.tar.xz" "$tarball"
    echo "  unpacking ffmpeg"
    rm -rf "$work/ffmpeg" && mkdir -p "$work/ffmpeg"
    tar -xJf "$tarball" -C "$work/ffmpeg" --strip-components=1
    install -m 0755 "$work/ffmpeg/ffmpeg" "$appdir/usr/bin/ffmpeg"
    install -m 0755 "$work/ffmpeg/ffprobe" "$appdir/usr/bin/ffprobe"

    local ytdlp="$CACHE/yt-dlp$ytsuffix"
    download "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_linux$ytsuffix" "$ytdlp"
    install -m 0755 "$ytdlp" "$appdir/usr/bin/yt-dlp"
  fi

  # --- launcher, desktop entry, icon -------------------------------------
  sed -e "s/__DARKSERVE_VERSION__/$VERSION/" "$ROOT/packaging/AppRun" > "$appdir/AppRun"
  chmod 0755 "$appdir/AppRun"
  install -m 0644 "$ROOT/packaging/darkserve.desktop" "$appdir/darkserve.desktop"
  install -m 0644 "$ROOT/launcher/darkserve.png" "$appdir/darkserve.png"
  install -m 0644 "$ROOT/launcher/darkserve.png" "$appdir/usr/share/icons/hicolor/256x256/apps/darkserve.png"
  ln -sf darkserve.png "$appdir/.DirIcon"
  sed -e "s/@VERSION@/$VERSION/" "$ROOT/packaging/darkserve.metainfo.xml" \
    > "$appdir/usr/share/metainfo/darkserve.metainfo.xml"

  # --- the AppImage runtime ----------------------------------------------
  local runtime="$CACHE/runtime-$rtarch"
  download "https://github.com/AppImage/type2-runtime/releases/download/continuous/runtime-$rtarch" "$runtime"

  # --- squashfs + runtime = the AppImage ---------------------------------
  local squash="$work/darkserve.squashfs"
  rm -f "$squash"
  echo "  building the squashfs image ($COMPRESS)"
  mksquashfs "$appdir" "$squash" -root-owned -noappend -no-progress -no-xattrs \
    -comp "$COMPRESS" -b 128K >/dev/null

  local out="$DIST/DarkServe-$VERSION-$arch.AppImage"
  echo "  writing $out"
  # Write to a temporary name and rename: replacing a running AppImage in
  # place fails with "Text file busy".
  cat "$runtime" "$squash" > "$out.tmp"
  chmod 0755 "$out.tmp"
  mv -f "$out.tmp" "$out"

  # --- sanity check -------------------------------------------------------
  if [[ "$arch" == "$(uname -m)" ]]; then
    echo "  testing the launcher"
    # A throwaway home, so the test never touches the real data of the user
    # who runs the build.
    local testhome="$work/home"
    rm -rf "$testhome" && mkdir -p "$testhome"
    run_app() {
      env HOME="$testhome" \
          XDG_DATA_HOME="$testhome/.local/share" \
          XDG_CACHE_HOME="$testhome/.cache" \
          DARKSERVE_PORT=7799 \
          "$out" --appimage-extract-and-run "$@"
    }
    run_app --cli start >/dev/null
    run_app --cli status >/dev/null
    run_app --cli stop >/dev/null
    # Nothing may be left running after the test.
    if run_app --cli status | grep -q "RUNNING"; then
      echo "  the test server is still running!" >&2
      exit 1
    fi
    echo "  start / status / stop ✓"
  else
    echo "  (not testable on this machine, $(uname -m))"
  fi

  ls -lh "$out" | awk '{print "  " $5 "  " $9}'
}

for a in $ARCHES; do
  build_arch "$a"
done

echo
echo "done → $DIST"
