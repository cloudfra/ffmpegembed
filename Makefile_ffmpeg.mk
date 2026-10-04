# Makefile_ffmpeg.mk
#
# Downloads the static ffmpeg + ffprobe binaries that ffmpegembed links into
# its Go binary via go:embed (see internal/embedded/embed_*_*.go) and places
# them under internal/embedded/bin/<goos>_<goarch>/ so the package compiles.
#
# Sources: BtbN/FFmpeg-Builds "latest" static builds. One archive per
# GOOS/GOARCH pair; the binaries live at <archive-root>/bin/{ffmpeg,ffprobe}.
#
#   make ffembed            # download the binaries for ALL supported platforms
#   make ffembed-host       # download the binary for the current host platform
#   make ffembed-clean      # remove all downloaded binaries
#
# Overridables:
#   FF_VERSION   FFmpeg minor version to pin (default: 8.1)
#   FFMPEG_GPL   use the GPL build (default: 1). Set 0 for the LGPL build.
#   FF_BASE_URL  release download base (default: the BtbN "latest" tag)

FF_VERSION  ?= 8.1
FFMPEG_GPL  ?= 1
FF_BASE_URL ?= https://github.com/BtbN/FFmpeg-Builds/releases/download/latest

# GPL builds are tagged "gpl" in the asset name; LGPL builds are tagged "lgpl".
ifeq ($(FFMPEG_GPL),1)
  FF_LICENSE := gpl
else
  FF_LICENSE := lgpl
endif

FF_EMBED ?= internal/embedded/bin

# tuple: <embeddir>|<BtbN asset name>|<archive ext>
FF_TARGETS := \
  linux_amd64|linux64|tar.xz \
  linux_arm64|linuxarm64|tar.xz \
  windows_amd64|win64|zip \
  windows_arm64|winarm64|zip

FF_STAMPS := $(foreach t,$(FF_TARGETS),$(FF_EMBED)/$(firstword $(subst |, ,$(t)))/.done)

# Host platform detection. This Makefile is included by a Go project, so `go env`
# is available and is the authoritative source of GOOS/GOARCH.
FF_HE_GOOS   := $(shell go env GOOS 2>/dev/null || true)
FF_HE_GOARCH := $(shell go env GOARCH 2>/dev/null || true)
FF_HOST_NAME := $(FF_HE_GOOS)_$(FF_HE_GOARCH)

# Only treat the host as "embedded-capable" if it is one of the targets below;
# otherwise ffembed-host is a no-op with a hint (the library still works via
# Args.UseExternalIfAvailable or an inline binary override).
FF_HOST_STAMP_OK := $(filter $(FF_HOST_NAME),linux_amd64 linux_arm64 windows_amd64 windows_arm64)
ifneq ($(strip $(FF_HOST_STAMP_OK)),)
  FF_HOST_STAMP := $(FF_EMBED)/$(FF_HOST_NAME)/.done
endif

.PHONY: ffembed ffembed-host ffembed-clean ffembed-list

# Download binaries for every supported platform.
ffembed: $(FF_STAMPS)

# Download only the binary needed to build/run on the current host.
ffembed-host:
	@if [ -n "$(FF_HOST_STAMP)" ]; then \
		$(MAKE) --no-print-directory "$(FF_HOST_STAMP)" \
	; else \
		echo "  [ffmpeg] no embedded binary for host '$(FF_HOST_NAME)' (not in: linux_amd64 linux_arm64 windows_amd64 windows_arm64)"; \
		echo "            the library will fall back to Args.UseExternalIfAvailable or an inline override"; \
	fi

# Remove all downloaded binaries (they are go:embed inputs, not source).
ffembed-clean:
	rm -rf $(FF_STAMPS:%/.done=%)

# ---------------------------------------------------------------------------
# Per-platform download + extract. A `.done` stamp marks a platform complete;
# the binary files themselves are the deliverables that go:embed consumes.
# ---------------------------------------------------------------------------
define ff_platform_rule
$(FF_EMBED)/$(1)/.done:
	@set -e; \
	echo "  [ffmpeg] $(1) (BtbN n$(FF_VERSION), $(FF_LICENSE))"; \
	btb="$(2)"; \
	ext="$(3)"; \
	sfx=""; \
	case "$(1)" in windows*) sfx=".exe";; esac; \
	dest="$(FF_EMBED)/$(1)"; \
	url="$(FF_BASE_URL)/ffmpeg-n$(FF_VERSION)-latest-$(btb)-$(FF_LICENSE)-$(FF_VERSION).$(ext)"; \
	tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	rm -f "$(dest)/ffmpeg$$sfx" "$(dest)/ffprobe$$sfx" 2>/dev/null || true; \
	mkdir -p "$$dest"; \
	if [ "$$ext" = "zip" ]; then \
		curl -fLsS --retry 3 -o "$$tmp/a.zip" "$$url"; \
		( cd "$$tmp" && unzip -qq a.zip ); \
	else \
		curl -fLsS --retry 3 -o "$$tmp/a.tar.xz" "$$url"; \
		( cd "$$tmp" && tar xJf a.tar.xz ); \
	fi; \
	bindir="$$(cd "$$tmp" && find . -type f -name ffmpeg$$sfx -print -quit)"; \
	bindir="$$(dirname "$$bindir")"; \
	if [ ! -f "$$bindir/ffmpeg$$sfx" ] || [ ! -f "$$bindir/ffprobe$$sfx" ]; then \
		echo "  [ffmpeg] expected $$bindir/{ffmpeg,ffprobe}$$sfx — archive layout changed?" >&2; \
		exit 1; \
	fi; \
	cp "$$bindir/ffmpeg$$sfx" "$$dest/ffmpeg$$sfx"; \
	cp "$$bindir/ffprobe$$sfx" "$$dest/ffprobe$$sfx"; \
	chmod +x "$$dest/ffmpeg$$sfx" "$$dest/ffprobe$$sfx"; \
	touch "$@"
endef

$(foreach t,$(FF_TARGETS),$(eval $(call ff_platform_rule,$(firstword $(subst |, ,$(t))),$(secondword $(subst |, ,$(t))),$(thirdword $(subst |, ,$(t))))))

.PHONY: ffembed-list
ffembed-list:
	@echo "Supported embedded platforms:"
	@for t in $(FF_TARGETS); do \
		printf '  %-16s -> %s (%s)\n' "$$(echo $$t | cut -d'|' -f1)" "$$(echo $$t | cut -d'|' -f2)" "$$(echo $$t | cut -d'|' -f3)"; \
	done
