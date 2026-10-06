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
# It downloads every platform's ffmpeg, ffprobe and license text from that
# release and then:
#   * rewrites internal/download/manifest.json, the version -> URL + SHA-256
#     mapping the library uses to download and verify a build at runtime;
#   * for the embedded platforms, replaces the archive committed under
#     internal/embedded/bin/ (the go:embed input): one ffmpeg.tar.xz holding
#     ffmpeg, ffprobe and their LICENSE, compressed with xz at its highest
#     level. Both binaries go in one archive because they share most of their
#     code, which a dictionary larger than one binary lets xz store only once.
#
# Usage: scripts/update-ffmpeg.sh [RELEASE_TAG]
# RELEASE_TAG defaults to the version currently in the manifest, which makes a
# bare run a way to re-fetch and re-verify what is already pinned.
#
# Needs curl, sha256sum, GNU tar and xz. Review and commit the result. The
# embedded archives are ordinary git blobs (not Git LFS: the Go module proxy
# does not resolve LFS pointers, so LFS files would reach `go get` users as
# pointer text), so every update permanently adds about 45 MB to the
# repository history.

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

# xz at its highest level. The dictionary must be larger than one binary so the
# second one in the archive can be matched against the first; it is also the
# memory the decoder needs, so it is no larger than that requires.
XZ_OPTS=(-9e "--lzma2=preset=9e,dict=96MiB" -T1)

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
    fetch "$asset.LICENSE.gz"
    gunzip -c "$work/ffmpeg-$asset.gz" > "$work/ffmpeg"
    gunzip -c "$work/ffprobe-$asset.gz" > "$work/ffprobe"
    gunzip -c "$work/$asset.LICENSE.gz" > "$work/LICENSE"
    echo "    \"$goplat\": {"
    echo "      \"ffmpeg\": {\"file\": \"ffmpeg-$asset.gz\", \"sha256\": \"$(sha256 "$work/ffmpeg-$asset.gz")\", \"binary_sha256\": \"$(sha256 "$work/ffmpeg")\"},"
    echo "      \"ffprobe\": {\"file\": \"ffprobe-$asset.gz\", \"sha256\": \"$(sha256 "$work/ffprobe-$asset.gz")\", \"binary_sha256\": \"$(sha256 "$work/ffprobe")\"},"
    echo "      \"license\": {\"file\": \"$asset.LICENSE.gz\", \"sha256\": \"$(sha256 "$work/$asset.LICENSE.gz")\", \"binary_sha256\": \"$(sha256 "$work/LICENSE")\"}"
    echo "    }$comma"
    if is_embedded "$goplat"; then
      echo "  [ffmpeg] compressing $goplat" >&2
      mkdir -p "$EMBED_DIR/$goplat"
      rm -f "$EMBED_DIR/$goplat"/*.gz
      chmod 0755 "$work/ffmpeg" "$work/ffprobe"
      chmod 0644 "$work/LICENSE"
      # Fixed order, timestamps and ownership keep the archive reproducible.
      tar --format=ustar --mtime='2000-01-01 00:00:00Z' --owner=0 --group=0 --numeric-owner \
        -C "$work" -cf "$work/ffmpeg.tar" ffmpeg ffprobe LICENSE
      xz "${XZ_OPTS[@]}" -c "$work/ffmpeg.tar" > "$EMBED_DIR/$goplat/ffmpeg.tar.xz"
      cp "$work/LICENSE" "$EMBED_DIR/$goplat/LICENSE"
    fi
  done
  echo '  }'
  echo '}'
} > "$work/manifest.json"

mkdir -p "$(dirname "$MANIFEST")"
mv "$work/manifest.json" "$MANIFEST"
echo "  [ffmpeg] pinned $tag in $MANIFEST and $EMBED_DIR/{$(IFS=,; echo "${EMBEDDED[*]}")}" >&2
