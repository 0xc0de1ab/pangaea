# Kind E2E

This environment runs the Pangaea router in a local kind cluster.

## Codex Runtime

The Codex script deploys one Codex AppServer provider into kind as a Kubernetes
Deployment named `pangaea-codex-runtime`. The default provider type and instance id
are both `codex-cli`; set
`PANGAEA_PROVIDER_INSTANCE_ID` only when running multiple Codex accounts on the
same host.

The Pod has two containers:

- `runtime`: runs `codex app-server` on `ws://127.0.0.1:8080`
- `shim`: runs `pangaeactl provider-shim run` and connects to the runtime over
  the shared Pod network namespace

The Codex auth file is copied into the provider state volume by an initContainer.
It is not baked into any image. Source priority:

1. `PANGAEA_CODEX_AUTH_PATH`
2. `assets/.codex/auth.json`
3. `~/.codex/auth.json`

Run:

```bash
./scripts/e2e-kind-codex.sh
```

The local development router bearer key defaults to `1`. Enter `1` in the
dashboard bearer field, or override it with `PANGAEA_ROUTER_API_KEY` when
starting the script.

Required host tools are Docker, kind, and curl. If `kubectl` is missing, the
script downloads a version matching the kind control-plane into
`.tmp/kind-codex-e2e/kubectl`.

Useful overrides:

```bash
PANGAEA_CODEX_AUTH_PATH=/srv/pangaea/auth/codex/primary/auth.json \
PANGAEA_PROVIDER_INSTANCE_ID=codex-primary-a1 \
PANGAEA_ACCOUNT_HINT=primary@example.test \
./scripts/e2e-kind-codex.sh
```

The auth source is put into a Kubernetes Secret, then an initContainer copies it
into the provider state volume at `/var/lib/pangaea/auth/codex/auth.json`.
Neither Codex runtime nor shim reads the Secret mount directly.

The script verifies:

- router pod readiness in kind
- `pangaea-codex-runtime` Deployment rollout
- `runtime` and `shim` containers running in one Pod
- local provider image loaded into the kind node
- auth copy into `/var/lib/pangaea/auth/codex/auth.json`
- provider control and data WebSocket sessions on the router dashboard API

Storage mode defaults to persistent. The router writes state snapshots under
`/var/lib/pangaea/pangaea-router` inside the kind node. Codex provider state is
stored under `/var/lib/pangaea/<provider_instance_id>` inside the kind node and
mounted into the runtime Pod. Use ephemeral mode for CI or disposable GitHub
Workflow runs:

```bash
PANGAEA_E2E_STORAGE_MODE=ephemeral ./scripts/e2e-kind-codex.sh
```

Set `PANGAEA_E2E_REQUIRE_ROUTE=1` to fail if the `codex-default` route is not
allowed by policy and current auth state. `PANGAEA_E2E_INVOKE=1` attempts a real
OpenAI-compatible chat request through the Codex AppServer WebSocket adapter.

## Antigravity Runtime

The Antigravity script deploys Antigravity fully inside kind as a Kubernetes
Deployment:

- `bootstrap-antigravity` init container: creates the runtime state directory
  and copies `state.vscdb` into the Pod state volume when an auth source is
  available
- `runtime` container: Antigravity compat proxy and bundled local runtime
- `shim` container: `pangaeactl provider-shim run --sidecar`

Both containers share one Pod network namespace. The shim calls the runtime at
`http://127.0.0.1:8080` and keeps router control/data WebSockets open through
the cluster-local `pangaea-router` service.

Run:

```bash
./scripts/e2e-kind-antigravity.sh
```

By default it builds the runtime image from Pangaea's integrated
`providers/antigravity-runtime` source. The Antigravity server bundle itself is
not committed. Put a prepared bundle under
`providers/antigravity-runtime/server-bundle`, or point the script at one:

```bash
PANGAEA_ANTIGRAVITY_SERVER_BUNDLE_DIR=/path/to/server-bundle \
./scripts/e2e-kind-antigravity.sh
```

Antigravity auth bootstrap checks `PANGAEA_ANTIGRAVITY_AUTH_PATH` first, then
repo assets, Linux local state, and WSL Windows user state such as
`<wsl-windows-users-root>/<USER>/AppData/Roaming/Antigravity/User/globalStorage/state.vscdb`.
The source file is copied into the container state volume; it is not mounted
directly.

The script verifies:

- router pod readiness in kind
- `pangaea-antigravity-runtime` Deployment rollout
- Antigravity provider control and data WebSocket sessions
- `antigravity-default` routes for OpenAI, Anthropic, and Gemini dialects
- a buffered OpenAI-compatible router invocation
- `state.vscdb` account extraction into the provider `Account` field when auth
  state is present

If Antigravity auth state is absent, the current compat proxy can still return
its auth-fallback response. Set `PANGAEA_ANTIGRAVITY_REQUIRE_REAL=1` to fail the
run when that fallback is observed.
