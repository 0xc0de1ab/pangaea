# Build Guide

This document covers local builds, artifact layout, version stamping, and the current CI build behavior.

## Build Matrix

The repository uses a Makefile-driven build matrix.

Dimensions:

- OS: `linux`, `darwin`, `windows`
- architecture: `amd64`, `arm64`
- variant: `debug`, `release`

Supported selector styles:

- `make all`
- `make linux`
- `make arm64`
- `make release`
- `make linux-arm64`
- `make linux-release`
- `make arm64-release`
- `make linux-arm64-release`

Run this for a compact summary:

```bash
make help
```

## Common Commands

Build one Linux release binary:

```bash
make linux-amd64-release
```

Build all release binaries:

```bash
make release
```

Build everything:

```bash
make all
```

Remove generated artifacts:

```bash
make clean
```

## Artifact Layout

Artifacts are written under:

```text
build/<os-label>-<arch>/<variant>/
```

Examples:

- `build/linux-amd64/release/pangaeactl`
- `build/linux-arm64/debug/pangaeactl`
- `build/macos-arm64/release/pangaeactl`
- `build/windows-amd64/release/pangaeactl.exe`

Notes:

- `darwin` artifacts are written under `build/macos-<arch>/...`
- Windows artifacts include `.exe`

## Debug vs Release

`debug` builds:

- keep symbols
- use `-gcflags=all=-N -l`
- are better suited for debugging and local inspection

`release` builds:

- use `CGO_ENABLED=0`
- strip symbols with `-s -w`
- add `-extldflags -static`
- are intended to produce statically linked Linux binaries

## Version Stamping

The base repository version currently comes from:

```text
v0.9.0-202604.1
```

At build time the Makefile appends a short Git SHA through `scripts/version.sh`, producing:

```text
vSEMVER-YYYYMM.seq.<commit-sha>
```

Example:

```text
v0.9.0-202604.1.f9e994e562eb
```

The default short SHA length is 12 characters.

Relevant variables:

- `VERSION_BASE`
- `VERSION`
- `GIT_SHA`
- `SHA_LEN`

Examples:

```bash
VERSION_BASE=v0.9.1-202604.2 make linux-amd64-release
GIT_SHA=abcdef123456 make linux-amd64-release
SHA_LEN=8 make linux-amd64-release
```

## Test and Housekeeping Targets

Available non-build targets:

- `make test`
- `make race`
- `make integration`
- `make lint`
- `make fmt`
- `make vet`
- `make tidy`

Examples:

```bash
make test
make race
make vet
```

## CI Build Behavior

GitHub Actions currently:

- runs `go test ./...`
- measures coverage
- builds release artifacts for multiple targets
- updates the coverage badge on `main`

## Notes for Operators

- When comparing deployed binaries, prefer checking file hashes if the version string still reflects the last committed SHA.
- For Linux server deployments in this repository, `linux-arm64-release` and `linux-amd64-release` are the most commonly used targets.
- Build details are intentionally kept separate from deployment instructions; see [deploy.md](./deploy.md) for runtime setup.
