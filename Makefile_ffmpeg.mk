# Makefile_ffmpeg.mk
#
# Downloads the static ffmpeg + ffprobe binaries that ffmpegembed links into
# its Go binary via go:embed (see internal/embedded/embed_*_*.go) and places
# them under internal/embedded/bin/<goos>_<goarch>/ so the package compiles.
#
# Sources — the canonical static-build hosts linked from https://ffmpeg.org/download.html
# (all are the rolling "master"/"latest" track, so no version is pinned):
#   linux + windows -> BtbN/FFmpeg-Builds "master-latest" GPL builds.
#                      One archive per (os,arch); both binaries live at
#                      <archive-root>/bin/{ffmpeg,ffprobe}.
#   darwin          -> eugeneware/ffmpeg-static "latest" builds.
#                      One single-binary .gz per tool.
#
# To build against a specific release, override a FF_FFURL_*/FF_FPURL_* value
# on the make command line. To skip the download and use a local archive,
# override FF_DL.
#
#   make ffembed         # download the binaries for ALL supported platforms
#   make ffembed-host    # download only the host (GOOS/GOARCH) binary
#   make ffembed-clean   # remove downloaded binaries + temp work dir
#
# Temp/work files live under build/ (gitignored); the vendored binaries under
# internal/embedded/bin/ are also gitignored (they are go:embed inputs).
#
# Supported platforms: add FF_<VAR>_<goos>_<goarch> entries below to add one.

FF_EMBED ?= internal/embedded/bin
FF_WORK  := build/ffmpeg

FF_BTBN   := https://github.com/BtbN/FFmpeg-Builds/releases/download/latest
FF_STATIC := https://github.com/eugeneware/ffmpeg-static/releases/latest/download
FF_DL     := curl -fLsS --retry 3

# Per-(goos,goarch) embed data. kind "btbin" = one archive with bin/ffmpeg +
# bin/ffprobe; kind "static" = one .gz per tool (the file IS the binary).
# EXE is the output file suffix (".exe" on windows, empty elsewhere).
FF_KIND_linux_amd64    := btbin
FF_FFURL_linux_amd64   := $(FF_BTBN)/ffmpeg-master-latest-linux64-gpl.tar.xz
FF_FPURL_linux_amd64   := $(FF_BTBN)/ffmpeg-master-latest-linux64-gpl.tar.xz

FF_KIND_linux_arm64    := btbin
FF_FFURL_linux_arm64   := $(FF_BTBN)/ffmpeg-master-latest-linuxarm64-gpl.tar.xz
FF_FPURL_linux_arm64   := $(FF_BTBN)/ffmpeg-master-latest-linuxarm64-gpl.tar.xz

FF_KIND_linux_386      := static
FF_FFURL_linux_386     := $(FF_STATIC)/ffmpeg-linux-ia32.gz
FF_FPURL_linux_386     := $(FF_STATIC)/ffprobe-linux-ia32.gz

FF_KIND_linux_arm      := static
FF_FFURL_linux_arm     := $(FF_STATIC)/ffmpeg-linux-arm.gz
FF_FPURL_linux_arm     := $(FF_STATIC)/ffprobe-linux-arm.gz

FF_KIND_windows_amd64  := btbin
FF_FFURL_windows_amd64 := $(FF_BTBN)/ffmpeg-master-latest-win64-gpl.zip
FF_FPURL_windows_amd64 := $(FF_BTBN)/ffmpeg-master-latest-win64-gpl.zip
FF_EXE_windows_amd64   := .exe

FF_KIND_windows_arm64  := btbin
FF_FFURL_windows_arm64 := $(FF_BTBN)/ffmpeg-master-latest-winarm64-gpl.zip
FF_FPURL_windows_arm64 := $(FF_BTBN)/ffmpeg-master-latest-winarm64-gpl.zip
FF_EXE_windows_arm64   := .exe

FF_KIND_darwin_amd64   := static
FF_FFURL_darwin_amd64  := $(FF_STATIC)/ffmpeg-darwin-x64.gz
FF_FPURL_darwin_amd64  := $(FF_STATIC)/ffprobe-darwin-x64.gz

FF_KIND_darwin_arm64   := static
FF_FFURL_darwin_arm64  := $(FF_STATIC)/ffmpeg-darwin-arm64.gz
FF_FPURL_darwin_arm64  := $(FF_STATIC)/ffprobe-darwin-arm64.gz

# Platform list (must match the FF_KIND_* rows above).
FF_PLATFORMS := linux_amd64 linux_arm64 linux_386 linux_arm windows_amd64 windows_arm64 darwin_amd64 darwin_arm64
FF_STAMPS    := $(addprefix $(FF_EMBED)/,$(addsuffix /.done,$(FF_PLATFORMS)))

# Host detection. `go` is in PATH because this file is included by a Go project.
FF_HE_GOOS   := $(shell go env GOOS 2>/dev/null || true)
FF_HE_GOARCH := $(shell go env GOARCH 2>/dev/null || true)
FF_HOST_NAME := $(FF_HE_GOOS)_$(FF_HE_GOARCH)
FF_HOST_OK   := $(filter $(FF_HOST_NAME),$(FF_PLATFORMS))
ifneq ($(strip $(FF_HOST_OK)),)
  FF_HOST_STAMP := $(addprefix $(FF_EMBED)/,$(FF_HOST_NAME)/.done)
endif

.PHONY: ffembed ffembed-host ffembed-clean

# Download binaries for every supported platform.
ffembed: $(FF_STAMPS)

# Download only the host binary (the default `make` target when present).
ffembed-host:
	@if [ -n "$(FF_HOST_STAMP)" ]; then \
		$(MAKE) --no-print-directory "$(FF_HOST_STAMP)"; \
	else \
		echo "  [ffmpeg] no embedded binary for host '$(FF_HOST_NAME)'"; \
		echo "            library will fall back to Args.UseExternalIfAvailable or an inline override"; \
	fi

# Remove downloaded binaries + the temp work dir.
ffembed-clean:
	rm -rf $(addprefix $(FF_EMBED)/,$(FF_PLATFORMS)) $(FF_WORK)

# ---------------------------------------------------------------------------
# One pattern rule handles every platform. $* is the <goos>_<goarch> stem and
# the FF_{KIND,FFURL,FPURL,EXE}_$(stem) variables select the source + kind.
# A `.done` stamp is produced per platform so the recipe runs once each.
# ---------------------------------------------------------------------------
$(FF_EMBED)/%/.done:
	set -eu; \
	dest="$(FF_EMBED)/$*"; \
	kind="$(FF_KIND_$*)"; \
	ffurl="$(FF_FFURL_$*)"; \
	fpurl="$(FF_FPURL_$*)"; \
	exe="$(FF_EXE_$*)"; \
	work="$(FF_WORK)/$*"; \
	mkdir -p "$$work" "$$dest"; \
	rm -f "$$dest/ffmpeg$$exe" "$$dest/ffprobe$$exe" 2>/dev/null || true; \
	if [ "$$kind" = "static" ]; then \
		echo "  [ffmpeg] $*  <-  $$ffurl / $$fpurl"; \
		$(FF_DL) -o "$$work/ffmpeg.gz"  "$$ffurl"; \
		$(FF_DL) -o "$$work/ffprobe.gz" "$$fpurl"; \
		gunzip -c "$$work/ffmpeg.gz"  > "$$dest/ffmpeg$$exe"; \
		gunzip -c "$$work/ffprobe.gz" > "$$dest/ffprobe$$exe"; \
	else \
		echo "  [ffmpeg] $*  <-  $$ffurl"; \
		case $$ffurl in \
			*.zip)    $(FF_DL) -o "$$work/a.zip"  "$$ffurl" && ( cd "$$work" && unzip -qq a.zip ) ;; \
			*.tar.xz) $(FF_DL) -o "$$work/a.txz"  "$$ffurl" && ( cd "$$work" && tar xJf a.txz ) ;; \
			*) echo "  [ffmpeg] unsupported archive type for $*: $$ffurl" >&2; exit 1 ;; \
		esac; \
		bindir="$$(cd "$$work" && find . -type f -name "ffmpeg$$exe" -print -quit)"; \
		bindir="$$(dirname "$$bindir")"; \
		cp "$$work/$$bindir/ffmpeg$$exe"  "$$dest/ffmpeg$$exe"; \
		cp "$$work/$$bindir/ffprobe$$exe" "$$dest/ffprobe$$exe"; \
	fi; \
	chmod +x "$$dest/ffmpeg$$exe" "$$dest/ffprobe$$exe"; \
	rm -rf "$$work"; \
	touch "$@"
