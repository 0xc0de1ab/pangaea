# pangaeactl

![Coverage](.github/badges/coverage.svg)

`pangaeactl` is a single-binary Go tool for mediating WebSocket credential synchronization across nodes. The current implementation uses end-to-end mTLS; the design spec also documents a planned `jwt` auth mode for ingress-friendly deployments.

## Versioning

The build version format is:

`vSEMVER-YYYYMM.seq.<commit-sha>`

The initial version in this repository is:

`v0.9.0-202604.1.5878da97dbb8`

## CI

GitHub Actions runs on every push and pull request. The workflow:

- runs `go test ./...` with coverage
- cross-builds release artifacts for Linux, macOS, and Windows
- updates the coverage badge on pushes to `main`

## Build

```bash
make linux-amd64-release
make darwin-arm64-release
make windows-amd64-release
```

Artifacts are written under `build/<os-label>-<arch>/<variant>/`.

Examples:

- `build/linux-amd64/release/pangaeactl`
- `build/macos-arm64/release/pangaeactl`
- `build/windows-amd64/release/pangaeactl.exe`

## Ingress

Today the server expects direct `wss://` + mTLS. If you need Kubernetes ingress in front of it, the preferred deployment is TLS passthrough so the backend still sees the original client certificate.

The design spec also describes a planned `auth_mode=jwt` alternative for ingress-terminated setups. Even in that mode, the backend endpoint remains `wss://`; JWT replaces client authentication, not transport encryption. For ingress-nginx, the documented backend options are:

- SSL passthrough for end-to-end mTLS
- HTTPS re-encryption from ingress to backend for `jwt` mode

Details and example annotations live in [docs/design/specs.md](/workspace/claude-creds-share/docs/design/specs.md:1).
