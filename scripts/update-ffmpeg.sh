#!/usr/bin/env bash
#
# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Pins the ffmpeg/ffprobe builds this module ships and downloads to one
# eugeneware/ffmpeg-static release.
#
# It downloads every platform's ffmpeg and ffprobe from that release and then:
#   * rewrites internal/download/manifest.json, the version -> URL + SHA-256
#     mapping the library uses to download and verify a build at runtime;
#   * replaces the gzip-compressed binaries committed under
#     internal/embedded/bin/ (the go:embed inputs) for the embedded platforms,
#     along with the license text of each build.
#
# Usage: scripts/update-ffmpeg.sh [RELEASE_TAG]
# RELEASE_TAG defaults to the version currently in the manifest, which makes a
# bare run a way to re-fetch and re-verify what is already pinned.
#
# Review and commit the result. The embedded binaries are ordinary git blobs
# (not Git LFS: the Go module proxy does not resolve LFS pointers, so LFS files
# would reach `go get` users as pointer text), so every update permanently adds
# about 120 MB to the repository history.

set -euo pipefail

cd "$(dirname "$0")/.."

MANIFEST=internal/download/manifest.json
EMBED_DIR=internal/embedded/bin
REPO_URL=https://github.com/eugeneware/ffmpeg-static

# <goos>_<goarch>:<release asset platform name>
PLATFORMS=(
  linux_amd64:linux-x64
  linux_arm64:linux-arm64
  linux_386:linux-ia32
  linux_arm:linux-arm
  windows_amd64:win32-x64
  darwin_amd64:darwin-x64
  darwin_arm64:darwin-arm64
)
# Platforms whose binaries are committed and embedded. Keep in sync with the
# embed_<goos>_<goarch>.go files in internal/embedded.
EMBEDDED=(linux_amd64 windows_amd64)

tag="${1:-}"
if [[ -z "$tag" ]]; then
  tag="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$MANIFEST")"
fi
if [[ -z "$tag" ]]; then
  echo "usage: $0 RELEASE_TAG" >&2
  exit 1
fi
base="$REPO_URL/releases/download/$tag"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch() {
  echo "  [ffmpeg] $1" >&2
  curl -fLsS --retry 3 -o "$work/$1" "$base/$1"
}

sha256() {
  sha256sum "$1" | cut -d' ' -f1
}

is_embedded() {
  local p
  for p in "${EMBEDDED[@]}"; do
    [[ "$p" == "$1" ]] && return 0
  done
  return 1
}

{
  echo '{'
  echo "  \"version\": \"$tag\","
  echo '  "license": "GPL-3.0-or-later",'
  echo "  \"base_url\": \"$base\","
  echo '  "platforms": {'
  last=$((${#PLATFORMS[@]} - 1))
  for i in "${!PLATFORMS[@]}"; do
    goplat="${PLATFORMS[$i]%%:*}"
    asset="${PLATFORMS[$i]##*:}"
    fetch "ffmpeg-$asset.gz"
    fetch "ffprobe-$asset.gz"
    comma=','
    [[ "$i" == "$last" ]] && comma=''
    echo "    \"$goplat\": {"
    echo "      \"ffmpeg\": {\"file\": \"ffmpeg-$asset.gz\", \"sha256\": \"$(sha256 "$work/ffmpeg-$asset.gz")\"},"
    echo "      \"ffprobe\": {\"file\": \"ffprobe-$asset.gz\", \"sha256\": \"$(sha256 "$work/ffprobe-$asset.gz")\"}"
    echo "    }$comma"
    if is_embedded "$goplat"; then
      fetch "$asset.LICENSE.gz"
      mkdir -p "$EMBED_DIR/$goplat"
      cp "$work/ffmpeg-$asset.gz" "$EMBED_DIR/$goplat/ffmpeg.gz"
      cp "$work/ffprobe-$asset.gz" "$EMBED_DIR/$goplat/ffprobe.gz"
      gunzip -c "$work/$asset.LICENSE.gz" > "$EMBED_DIR/$goplat/LICENSE"
    fi
  done
  echo '  }'
  echo '}'
} > "$work/manifest.json"

mkdir -p "$(dirname "$MANIFEST")"
mv "$work/manifest.json" "$MANIFEST"
echo "  [ffmpeg] pinned $tag in $MANIFEST and $EMBED_DIR/{$(IFS=,; echo "${EMBEDDED[*]}")}" >&2
