# Makefile_ffmpeg.mk
#
# The xz-compressed archives of ffmpeg + ffprobe that ffmpegembed links into
# its Go binary via go:embed are committed under internal/embedded/bin/, so
# there is nothing to download before building: `go get` and `go build` just
# work.
#
# This file only holds the maintainer target that moves the module to another
# ffmpeg release, using the mkffmpegembed tool (cmd/mkffmpegembed). It
# regenerates the manifest (internal/manifest/default.json: version -> URL +
# SHA-256), then rebuilds the archive of every embedded platform from the
# files that manifest pins. It needs the xz program.
#
#   make ffembed-update                    # re-fetch + re-verify the pinned release
#   make ffembed-update FF_VERSION=b6.1.1  # pin a different release tag

FF_VERSION ?=
FF_MANIFEST := internal/manifest/default.json
FF_EMBED := internal/embedded/bin
# Platforms with a committed archive. Keep in sync with the
# embed_<goos>_<goarch>.go files in internal/embedded.
FF_EMBEDDED := linux_amd64 windows_amd64
FF_MK := go run ./cmd/mkffmpegembed

.PHONY: ffembed-update

ffembed-update:
	$(FF_MK) manifest -from $(FF_MANIFEST) $(if $(FF_VERSION),-version $(FF_VERSION)) -o $(FF_MANIFEST)
	set -eu; for p in $(FF_EMBEDDED); do \
		$(FF_MK) archive -manifest $(FF_MANIFEST) -platform $$p -o $(FF_EMBED)/$$p/ffmpeg.tar.xz; \
		xz -dc $(FF_EMBED)/$$p/ffmpeg.tar.xz | tar -xOf - LICENSE > $(FF_EMBED)/$$p/LICENSE; \
	done
