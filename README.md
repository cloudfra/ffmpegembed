# template-go

<!-- markdownlint-disable-next-line MD033 -->
<img src="logo.png" alt="Logo" width="64" height="64" />

[![CI](https://github.com/cloudfra/template-go/actions/workflows/deploy.yaml/badge.svg)](https://github.com/cloudfra/template-go/actions/workflows/deploy.yaml) [![Go Reference](https://pkg.go.dev/badge/github.com/cloudfra/template-go.svg)](https://pkg.go.dev/github.com/cloudfra/template-go) [![codecov](https://codecov.io/gh/cloudfra/template-go/graph/badge.svg?token=UVApxhg6z7)](https://codecov.io/gh/cloudfra/template-go) [![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/cloudfra/template-go/badge)](https://scorecard.dev/viewer/?uri=github.com/cloudfra/template-go)

A starter template for Go projects at Cloudfra, wiring together a
`make`-based build system, cross-platform binaries, Docker images,
Terraform-based deployment, and CI so new services can skip the
boilerplate and start with working infrastructure on day one.

## Features

- **Cross-compilation** — build binaries for every supported OS/arch
  (Linux, Windows, macOS, BSDs, and more) with a single `make` invocation.
- **Signed release binaries** — `make release-binaries` code-signs the
  artifacts it can (all Windows architectures via Authenticode; only
  `linux/386` and `linux/amd64` via an embedded detached signature) with
  a self-signed cert generated on demand, or your own via
  `CODESIGN_CERT`/`CODESIGN_KEY`. Every other platform is copied unsigned.
- **Docker images** — package any binary defined under `cmd/` into a
  container image, either a quick single-arch build or a full
  multi-arch manifest merging every supported Linux/Windows platform.
- **Protobuf/gRPC support** — `proto.mk` and vendored `google_protobuf`
  and `grpc_gateway` definitions under `third_party/` for services that
  need RPC APIs.
- **Terraform deployment** — infrastructure-as-code under `install/terraform`
  for provisioning test and production environments.
- **CI included** — GitHub Actions workflow (`.github/workflows/deploy.yaml`)
  that builds, lints, and tests on every push and pull request.
- **Enforced linting** — `make lint` runs gofmt, gofumpt, golangci-lint,
  revive, hadolint, actionlint, govulncheck, tflint/terraform fmt, and
  trivy config (Terraform misconfiguration scanning), downloading its own
  toolchain so it's reproducible locally and in CI.

## Getting started

```bash
# Clone the repository
git clone git@github.com:cloudfra/template-go.git
# Build binaries for every supported platform
make -j$(nproc)
# Build and run the exampleapp binary for your current platform
make run
```

## Project layout

```bash
cmd/<app>/          Entry point(s) for each binary (one directory per app)
internal/           Private application and library code
install/terraform/  Infrastructure-as-code for deploying the app
third_party/        Vendored protobuf/gRPC-gateway definitions
build/              Build outputs (binaries, toolchain) - not checked in
```

To add a new binary, create a new directory under `cmd/` with a `main`
package; the build system picks it up automatically.

## Common make targets

| Target                             | Description                                                                                                    |
| ---------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `make` / `make all`                | Build binaries for every supported platform                                                                    |
| `make run`                         | Build and run the exampleapp binary                                                                               |
| `make test`                        | Run the unit test suite                                                                                        |
| `make bench`                       | Run benchmarks                                                                                                 |
| `make test-deflake`                | Re-run tests to catch flakes                                                                                   |
| `make lint`                        | Run the full lint suite (see Features)                                                                         |
| `make protos`                      | Generate code from `.proto` definitions                                                                        |
| `make docker-images`               | Build a quick single-arch (`linux/amd64`) Docker image per app, tagged locally                                 |
| `make scan-images`                 | Build a local single-arch image per app and scan it with trivy, failing on HIGH/CRITICAL vulnerabilities       |
| `make images`                      | Build every supported Linux/Windows platform and merge them into one multi-arch manifest per app               |
| `make linux-images windows-images` | Build just the Linux or Windows platform images that `make images` merges                                      |
| `make release-binaries`            | Build release artifacts for every platform, code-signing where supported (Windows, `linux/386`, `linux/amd64`) |
| `make windows-binaries`            | Build Windows binaries only                                                                                    |
| `make presubmit`                   | Run the full suite of checks used in CI (tools, lint, build, test-deflake)                                     |
| `make clean`                       | Remove build outputs                                                                                           |
| `make deps`                        | Install / upgrade Go module dependencies                                                                       |

## Testing

```bash
make test
make bench
```

## Simulating CI locally

[`gh act`](https://github.com/nektos/gh-act) (a `gh` CLI extension wrapping
[nektos/act](https://github.com/nektos/act)) runs this repo's actual
`.github/workflows/*.yaml` files locally in Docker, rather than
reimplementing CI's logic elsewhere:

```bash
gh extension install nektos/gh-act
gh act pull_request -W .github/workflows/reviewdog.yaml --secret GITHUB_TOKEN=$(gh auth token)
```

**Known limitations, both confirmed by hand:**

- `deploy.yaml` (the main build/lint/test/release pipeline) currently fails
  to parse under `act`: its schema validator doesn't yet recognize the
  `code-quality` permission scope (the same very-new GitHub Actions feature
  `make lint-yaml` already has to `-ignore` for `actionlint`), and that one
  unknown property cascades into spurious "Unknown Property" errors for the
  rest of the job. It also targets `runs-on: [self-hosted, ...]` runners,
  which `act` can't resolve without an explicit `-P <label>=<image>`
  mapping even once the schema issue is fixed.
- `reviewdog.yaml` parses and its jobs really execute (pulls the real
  action code, runs it in Docker) - but steps that call back to the GitHub
  API, like reviewdog posting check results, need a real `GITHUB_TOKEN`
  *and* correct repo/event context to fully succeed, not just the token
  shown above.

## Code signing

`make release-binaries` code-signs the release artifacts under
`build/release/` where signing is supported, and copies everything else
unsigned:

- **Windows** (all architectures) — Authenticode-signed with `osslsigncode`.
- **`linux/386` and `linux/amd64`** — get a detached CMS/PKCS7 signature
  embedded in a `.cloudfra_signature` ELF section via `objcopy`.
- **Everything else** (the remaining Linux architectures — arm, arm64,
  mips\*, ppc\*, riscv64, loong64, s390x — plus darwin and the BSDs) is a
  plain unsigned copy. Debian/Ubuntu's `binutils` package ships `objcopy`
  built with only the x86 BFD backends, so it can't embed a signature
  section into non-x86 ELF binaries (see `LINUX_OBJCOPY_SIGNABLE_PLATFORMS`
  in the `Makefile`).

Both signing paths require `osslsigncode`, `openssl`, and `binutils`
(`objcopy`) to be installed — see the "Install Signing Dependencies" step
in `deploy.yaml` for the packages CI installs.

By default the cert/key pair is generated on demand at
`build/certs/codesign.{crt,key}` using
[`certtool`](https://github.com/cloudfra/certtool) (self-signed, fetched
automatically as part of the toolchain). To sign with your own
certificate instead, point `CODESIGN_CERT`/`CODESIGN_KEY` at existing
files:

```bash
make CODESIGN_CERT=/path/to/cert.pem CODESIGN_KEY=/path/to/key.pem release-binaries
```

## Deployment

Infrastructure is managed with Terraform under `install/terraform`:

```bash
cd install/terraform
terraform init
terraform plan -var=gcp_project_id=<your-project-id> -var=testing=true
terraform apply -var=gcp_project_id=<your-project-id> -var=testing=true
```

## GitHub repository setup

`scripts/configure-github-repo.sh` applies the GitHub repository settings
this template expects (branch cleanup on merge, auto-merge, Dependabot
alerts/security updates, secret scanning, branch protection on `main`,
etc.), so a repo created from this template can be brought in line with
one command instead of clicking through Settings by hand. It infers
`OWNER/REPO` from the `origin` remote, so it's enough to just run:

```bash
scripts/configure-github-repo.sh
```

Pass `--repo OWNER/REPO` to target a different repository instead, or
`--dry-run` to print what it would change without touching anything.

It's idempotent, and settings unavailable on a given plan (e.g. branch
protection or secret scanning on a private repo without GitHub Advanced
Security) are skipped with a warning rather than failing the run.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
