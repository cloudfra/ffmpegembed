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

REGISTRY = ghcr.io/cloudfra

# Protobuf messages are generated once and checked in (proto/ffrun.pb.go).
# `make protos` regenerates them (pulls the protoc toolchain); the app build
# itself does not depend on them so it works offline against the checked-in
# generated code.
PROTOS = proto/ffrun.pb.go
TEST_ASSETS =
ASSETS =
GO_PACKAGE = github.com/cloudfra/ffmpegembed
ALL_APPS = ffrun
PRODUCTION=1

# Default entry point: build the app (which pulls the host's embedded binary).
.DEFAULT_GOAL := all

include Makefile_ffmpeg.mk
include Makefile_build.mk

# Build entry points pull the current host's embedded ffmpeg/ffprobe first so
# go:embed resolves. (Cross platforms are covered by `make ffembed`.)
all: ffembed-host
