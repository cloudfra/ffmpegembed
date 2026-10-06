# Makefile_ffmpeg.mk
#
# The gzip-compressed ffmpeg + ffprobe binaries that ffmpegembed links into its
# Go binary via go:embed are committed under internal/embedded/bin/, so there
# is nothing to download before building: `go get` and `go build` just work.
#
# This file only holds the maintainer target that moves the module to another
# ffmpeg release. It re-downloads every platform's build of an
# eugeneware/ffmpeg-static release, rewrites the download manifest
# (internal/download/manifest.json: version -> URL + SHA-256) and replaces the
# committed binaries. See scripts/update-ffmpeg.sh.
#
#   make ffembed-update                    # re-fetch + re-verify the pinned release
#   make ffembed-update FF_VERSION=b6.1.1  # pin a different release tag

FF_VERSION ?=

.PHONY: ffembed-update

ffembed-update:
	scripts/update-ffmpeg.sh $(FF_VERSION)
