# Kind Codex E2E

This environment runs the Pangaea router in a local kind cluster and starts one
Codex AppServer provider container through the host Docker runtime. The default
provider id and instance id are both `codex-cli`; set
`PANGAEA_PROVIDER_INSTANCE_ID` only when running multiple Codex accounts on the
same host.

The Codex auth file is copied into the provider container. It is not mounted and
is not baked into any image. Source priority:

1. `assets/.codex/auth.json`
2. `~/.codex/auth.json`

Run:

```bash
./scripts/e2e-kind-codex.sh
```

Required host tools are Docker, kind, curl, and Go. If `kubectl` is missing,
the script downloads a version matching the kind control-plane into
`.tmp/kind-codex-e2e/kubectl`.

Useful overrides:

```bash
PANGAEA_CODEX_AUTH_PATH=/srv/pangaea/auth/codex/samtest/auth.json \
PANGAEA_PROVIDER_ID=codex-samtest \
PANGAEA_PROVIDER_INSTANCE_ID=codex-samtest-a1 \
PANGAEA_ACCOUNT_HINT=samtest4u@gmail.com \
./scripts/e2e-kind-codex.sh
```

The script verifies:

- router pod readiness in kind
- provider container creation through node-agent reconcile
- local provider image usage with `image_pull_policy: never`
- auth copy into `/var/lib/pangaea/auth/codex/auth.json`
- provider control and data WebSocket sessions on the router dashboard API

Storage mode defaults to persistent. The router writes state snapshots under
`/var/lib/pangaea/router` inside the kind node, and provider containers bind
`/var/lib/pangaea` plus `/work` to
`.tmp/kind-codex-e2e/persistent/providers/<provider_instance_id>/` on the host.
Use ephemeral mode for CI or disposable GitHub Workflow runs:

```bash
PANGAEA_E2E_STORAGE_MODE=ephemeral ./scripts/e2e-kind-codex.sh
```

Set `PANGAEA_E2E_REQUIRE_ROUTE=1` to fail if the `codex-default` route is not
allowed by policy and current auth state. `PANGAEA_E2E_INVOKE=1` attempts a real
OpenAI-compatible chat request through the Codex AppServer WebSocket adapter.
