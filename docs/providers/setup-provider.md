# Setup Provider Command

`pangaeactl setup-provider` generates a consistent provider runtime setup for
host-native, Docker, Podman, kind, and Kubernetes targets.

## Runtime Types And Provider Modes

`--type` selects where the provider runtime is maintained:

- `native-systemd`: writes a provider env file and systemd unit.
- `docker`: writes a node-agent config and can reconcile a Docker container
  with `--apply`.
- `podman`: same as Docker, using the Podman CLI.
- `kind`: writes a Kubernetes Secret and Deployment manifest with kind-oriented
  defaults.
- `k8s` / `kubernetes`: writes a generic Kubernetes Secret and Deployment
  manifest.

`--mode` selects how the provider shim reaches the target provider process:

- `app-server`: start and use the provider's local app-server. This is the
  default for Codex and currently maps to Codex AppServer WebSocket transport.
- `http-direct`: make provider-native HTTP requests from the shim. This is the
  default for Gemini and is also available for Codex through Pangaea's native
  Codex Responses transport.
- `cli-adapter`: invoke the provider CLI per request. This is the default for
  Claude and is also available for Gemini.
- `sdk`: use the GitHub Copilot SDK through a local OpenAI-compatible relay.
  This is the default for GitHub Copilot.
- `acp`: use a provider CLI's ACP JSON-RPC transport. Currently supported for
  GitHub Copilot, Cursor, and Grok Build.
- `ls-core-sidecar`: reserved for Antigravity ls-core sidecar transport.

## Identity Rules

The command always separates host identity from container identity:

- `--host-name` is the physical/operator-facing host name sent to the router.
  It is optional; by default `setup-provider` reads the local OS hostname on
  the machine where the command runs.
- `--node-id` is optional. If omitted, `setup-provider` creates a stable
  six-digit numeric id per provider instance. The generated id is
  stored under
  `~/.config/pangaea/setup-provider/<type>/<service>/<provider-instance>/runtime.json`
  and reused on later setup runs. Existing six-character lower-case base36 ids
  remain accepted when already present in persisted runtime settings.
- Docker/Podman setup injects `PANGAEA_CONTAINER_KIND` and the deterministic
  container name. The shim falls back to runtime `HOSTNAME` as container id
  when no explicit id is available.
- kind/Kubernetes setup uses Downward API values for container metadata:
  `metadata.uid` for container id and `metadata.name` for container name.
  It still captures `spec.nodeName` as `PANGAEA_HOST_HOSTNAME` for diagnostics,
  but `PANGAEA_HOST_NAME` defaults to the setup host name, not the pod or kind
  node name.
- Container runtimes also receive the stable node id in
  `PANGAEA_NODE_ID` and record it inside the provider state volume at
  `/var/lib/pangaea/runtime/provider.env` so container restarts keep the same
  identity.

This avoids container hostnames appearing as provider hosts in the dashboard.

## Auth Rules

`setup-provider` does not accept an account label. If `--auth-path` is set, the
command parses that auth file with the provider format and derives the account
display from the file itself.

If `--auth-path` is omitted, no auth file is copied into the runtime. The
provider still starts and registers, but it reports `no_login`, does not
advertise auth file or refresh capabilities, and is not eligible for healthy
auth routing policies.

## Examples

Generate a Docker Gemini provider for a specific account:

```bash
pangaeactl setup-provider \
  --type docker \
  --mode http-direct \
  --service gemini \
  --auth-path ~/.gemini/oauth_creds.json
```

Create or update the Docker container immediately:

```bash
pangaeactl setup-provider \
  --type docker \
  --mode http-direct \
  --service gemini \
  --auth-path ~/.gemini/oauth_creds.json \
  --router-control ws://router.example/router/v1/control/ws \
  --apply
```

Generate a kind manifest:

```bash
pangaeactl setup-provider \
  --type kind \
  --mode http-direct \
  --service gemini \
  --auth-path ~/.gemini/oauth_creds.json \
  --namespace pangaea-e2e
```

Generate a no-login provider skeleton:

```bash
pangaeactl setup-provider \
  --type kind \
  --mode http-direct \
  --service gemini \
  --namespace pangaea-e2e
```

Use `--storage ephemeral` for CI-style non-persistent runtime state.

Generate a Grok Build ACP provider from cached SuperGrok login state:

```bash
pangaeactl setup-provider \
  --type k8s \
  --service grok-build \
  --mode acp \
  --auth-path ~/.grok/auth.json
```
