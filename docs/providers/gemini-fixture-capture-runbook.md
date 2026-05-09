# Gemini Fixture Capture Runbook

This runbook covers safe operation of the 300-case Gemini CLI fixture capture
through mitmproxy. The capture is intentionally long-running and consumes real
Gemini account quota. Do not run it from CI, shared terminals, or unattended
hosts.

## Scope

Use the repository helper:

```bash
PANGAEA_GEMINI_FIXTURE_LIMIT=300 \
PANGAEA_GEMINI_FIXTURE_MITM=1 \
scripts/capture-gemini-cli-fixtures.sh
```

The script captures:

- 300 Gemini CLI prompt cases across the configured model list.
- CLI request metadata, stdout NDJSON or JSON, stderr, argv, and result files.
- One ACP JSON-RPC probe after the CLI loop completes.
- One optional mitmproxy flow archive at `http/flows.mitm`.

The default output directory is `.tmp/gemini-fixtures`.

## Prerequisites

Run from the repository root:

```bash
cd "$(git rev-parse --show-toplevel)"
```

Required local tools:

- `bash`
- `node`
- `gemini`
- `timeout` from GNU coreutils, strongly recommended for per-case bounds
- `mitmdump` from mitmproxy
- `jq`, useful for validation and triage

Check the tools before the capture:

```bash
command -v gemini
command -v node
command -v mitmdump
command -v timeout
command -v jq
gemini --version
mitmdump --version
node --version
```

The script creates a controlled fixture workspace under the output directory.
It writes temporary fixture files there, not in the repository source tree.

## Auth And Quota Checks

Use a Gemini account that is dedicated to fixture capture or has explicit
approval for quota burn. Do not use a production operator account unless the
quota owner has approved the run window.

Before the capture:

```bash
test -s "${HOME}/.gemini/oauth_creds.json"
jq -r '.expiry_date, (.scope // "")' "${HOME}/.gemini/oauth_creds.json"
```

The auth file must contain at least `access_token`, `refresh_token`, and a
future `expiry_date`. If the token is missing, expired, or within a few minutes
of expiry, refresh it with the official Gemini CLI before starting the capture.

Run a one-case smoke test before the 300-case run:

```bash
rm -rf .tmp/gemini-fixtures-smoke
PANGAEA_GEMINI_FIXTURE_DIR=.tmp/gemini-fixtures-smoke \
PANGAEA_GEMINI_FIXTURE_LIMIT=1 \
PANGAEA_GEMINI_FIXTURE_MODELS=gemini-2.5-flash \
PANGAEA_GEMINI_FIXTURE_MITM=auto \
PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT=180 \
scripts/capture-gemini-cli-fixtures.sh
```

Investigate auth, TLS, model availability, and quota errors during the smoke
test. Do not start the 300-case run until the smoke test produces one CLI
result file and an ACP result.

Quota guidance:

- The default 300-case run exercises six models round-robin.
- Treat all 300 CLI cases plus the ACP probe as real billable or quota-counted
  operations.
- If account quota is uncertain, narrow the first real capture to a smaller
  model list or a smaller `PANGAEA_GEMINI_FIXTURE_LIMIT`, then schedule the full
  run after quota reset.

## Environment Variables

Primary controls:

```bash
export PANGAEA_GEMINI_FIXTURE_DIR="${PWD}/.tmp/gemini-fixtures"
export PANGAEA_GEMINI_FIXTURE_LIMIT=300
export PANGAEA_GEMINI_FIXTURE_MITM=1
export PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT=180
export PANGAEA_GEMINI_MITM_PORT=18089
```

Model coverage defaults to:

```bash
export PANGAEA_GEMINI_FIXTURE_MODELS=gemini-2.5-flash,gemini-2.5-pro,gemini-2.5-flash-lite,auto-gemini-2.5,gemini-3-pro-preview,gemini-3-flash-preview
```

Use `PANGAEA_GEMINI_FIXTURE_MITM=1` for the official capture so absence of
`mitmdump` fails fast. Use `auto` only for smoke runs where HTTP flows are not
required.

The script sets `HTTP_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY` after starting
mitmproxy. If `${HOME}/.mitmproxy/mitmproxy-ca-cert.pem` exists, it also sets
`NODE_EXTRA_CA_CERTS` so Node-based Gemini CLI traffic can trust the
intercepting CA.

## Expected Duration

Each CLI case is bounded by `PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT`, default
180 seconds. The worst-case upper bound for 300 cases is about 15 hours, plus
the ACP probe.

Typical successful runs should be much shorter, but plan an operator window of
2 to 8 hours depending on model latency, quota throttling, and network
conditions. If many cases reach the timeout, stop the run and triage instead of
letting it consume the whole worst-case window.

## Safe Run Procedure

1. Confirm no other process is using the mitmproxy port:

```bash
ss -ltnp | grep ':18089' || true
```

2. Start from a clean output directory for the official run:

```bash
rm -rf .tmp/gemini-fixtures
mkdir -p .tmp
```

3. Run from a terminal multiplexer or another persistent session:

```bash
PANGAEA_GEMINI_FIXTURE_DIR="${PWD}/.tmp/gemini-fixtures" \
PANGAEA_GEMINI_FIXTURE_LIMIT=300 \
PANGAEA_GEMINI_FIXTURE_MITM=1 \
PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT=180 \
PANGAEA_GEMINI_MITM_PORT=18089 \
scripts/capture-gemini-cli-fixtures.sh
```

4. Do not interrupt the process unless errors are clearly systemic. If
interrupted, keep the partial output directory for triage and start the next
attempt in a new directory.

## Monitoring

In a second terminal, watch progress:

```bash
watch -n 30 'find .tmp/gemini-fixtures/cli -name "*.result.json" 2>/dev/null | wc -l'
```

Inspect recent results:

```bash
find .tmp/gemini-fixtures/cli -name "*.result.json" -print | sort | tail -10 | xargs -r jq -c .
```

Watch stderr for systemic failures:

```bash
find .tmp/gemini-fixtures/cli -name "*.stderr.log" -print | sort | tail -10 | xargs -r tail -n 20
```

Watch mitmproxy:

```bash
tail -f .tmp/gemini-fixtures/http/mitmdump.log
```

Healthy signs:

- Result file count increases steadily.
- Exit codes are mostly `0`.
- `http/flows.mitm` grows during active traffic.
- `mitmdump.log` does not repeatedly report TLS verification or proxy errors.

Stop and investigate if:

- Multiple consecutive cases time out.
- All models return auth, quota, TLS, or model-not-found errors.
- `flows.mitm` remains empty after several completed cases with mitm enabled.
- The process stalls with no new result files for longer than one case timeout.

## Timeout And Retry Triage

Each CLI result file contains the case index, model, exit code, output format,
and approval mode. Stderr and stdout use the same prefix.

Summarize exit codes:

```bash
jq -r '.exit_code' .tmp/gemini-fixtures/cli/*.result.json | sort | uniq -c
```

Find non-zero cases:

```bash
for f in .tmp/gemini-fixtures/cli/*.result.json; do
  jq -e '.exit_code != 0' "$f" >/dev/null || continue
  echo "$f"
  jq -c . "$f"
  sed -n '1,80p' "${f%.result.json}.stderr.log"
done
```

Retry only targeted cases manually after triage. The capture script does not
provide a single-case retry flag, so the safest retry pattern is to run a small
new capture into a separate directory with the same model narrowed via
`PANGAEA_GEMINI_FIXTURE_MODELS`, then keep both directories for comparison.

Use a new directory for retries:

```bash
PANGAEA_GEMINI_FIXTURE_DIR="${PWD}/.tmp/gemini-fixtures-retry-001" \
PANGAEA_GEMINI_FIXTURE_LIMIT=6 \
PANGAEA_GEMINI_FIXTURE_MODELS=gemini-2.5-flash \
PANGAEA_GEMINI_FIXTURE_MITM=1 \
scripts/capture-gemini-cli-fixtures.sh
```

Do not overwrite the official output directory during triage.

## Artifact Layout

Expected official output:

```text
.tmp/gemini-fixtures/
  manifest.json
  workspace/
    GEMINI.md
    .gemini/settings.json
    fixtures/
      sample.go
      sample.md
      data.json
      tiny.png
    mcp-server.mjs
  cli/
    001_<model>.request.json
    001_<model>.argv.txt
    001_<model>.stdout.ndjson
    001_<model>.stderr.log
    001_<model>.result.json
    ...
  acp/
    rpc.ndjson
    stderr.log
    stdout-noise.log
    summary.json
  http/
    flows.mitm
    mitmdump.log
```

The `cli` directory should contain five files per case. For 300 cases, expect:

- 300 `*.request.json`
- 300 `*.argv.txt`
- 300 `*.stdout.ndjson`
- 300 `*.stderr.log`
- 300 `*.result.json`

The stdout extension remains `.stdout.ndjson` even for cases where the CLI
output format is `json`.

## Post-Run Validation

Run these commands after the script exits successfully:

```bash
test -s .tmp/gemini-fixtures/manifest.json
jq . .tmp/gemini-fixtures/manifest.json
find .tmp/gemini-fixtures/cli -name "*.result.json" | wc -l
find .tmp/gemini-fixtures/cli -name "*.request.json" | wc -l
find .tmp/gemini-fixtures/cli -name "*.stdout.ndjson" | wc -l
find .tmp/gemini-fixtures/cli -name "*.stderr.log" | wc -l
test -s .tmp/gemini-fixtures/http/flows.mitm
test -s .tmp/gemini-fixtures/http/mitmdump.log
```

Validate manifest values:

```bash
jq -e '
  .target == "gemini-cli" and
  .adapter == "cli-adapter" and
  .direct_http_name == "direct-http" and
  .limit == 300 and
  (.case_timeout_seconds | type == "number")
' .tmp/gemini-fixtures/manifest.json
```

Validate result counts and exit code distribution:

```bash
results=$(find .tmp/gemini-fixtures/cli -name "*.result.json" | wc -l)
test "$results" -eq 300
jq -r '.exit_code' .tmp/gemini-fixtures/cli/*.result.json | sort | uniq -c
```

Validate every result has its sibling files:

```bash
for f in .tmp/gemini-fixtures/cli/*.result.json; do
  p="${f%.result.json}"
  test -s "${p}.request.json"
  test -s "${p}.argv.txt"
  test -e "${p}.stdout.ndjson"
  test -e "${p}.stderr.log"
done
```

Check model spread:

```bash
jq -r '.model' .tmp/gemini-fixtures/cli/*.result.json | sort | uniq -c
```

Check output format spread:

```bash
jq -r '.output_format' .tmp/gemini-fixtures/cli/*.result.json | sort | uniq -c
```

Check ACP artifacts exist:

```bash
find .tmp/gemini-fixtures/acp -maxdepth 2 -type f -print | sort
test -s .tmp/gemini-fixtures/acp/rpc.ndjson
test -s .tmp/gemini-fixtures/acp/summary.json
jq . .tmp/gemini-fixtures/acp/summary.json
```

If mitmproxy tooling is available, inspect the flow count:

```bash
mitmdump -nr .tmp/gemini-fixtures/http/flows.mitm >.tmp/gemini-fixtures/http/flows.txt
wc -l .tmp/gemini-fixtures/http/flows.txt
sed -n '1,40p' .tmp/gemini-fixtures/http/flows.txt
```

Do not paste raw flow contents into issues or chat. HTTP flows may contain
provider headers, prompts, responses, or account-linked metadata.

## Acceptable Failures

Acceptable, with documentation in the handoff:

- A small number of isolated non-zero CLI exits where stderr shows transient
  upstream `429`, `503`, network reset, or timeout behavior.
- Preview model unavailability for `gemini-3-pro-preview` or
  `gemini-3-flash-preview`, if stable models in the same run succeed.
- Individual MCP or image prompt failures when plain text prompts and file
  injection prompts continue to succeed.
- Empty or near-empty stderr logs for successful cases.
- ACP `session/close` absence, because the probe does not require it by
  default.

For acceptable failures, preserve the failed case artifacts and include:

- Case prefix.
- Model.
- Exit code.
- First relevant stderr lines.
- Whether retry succeeded in a separate retry directory.

## Failures Requiring Investigation

Investigate before accepting the run if any of these occur:

- Missing `manifest.json`.
- Fewer than 300 CLI result files for a completed official run.
- Missing request, argv, stdout, or stderr sibling files for any result.
- `http/flows.mitm` missing or empty when `PANGAEA_GEMINI_FIXTURE_MITM=1`.
- Repeated TLS errors, proxy connection failures, or CA trust errors in
  `mitmdump.log`.
- All or most cases for a model family fail.
- All cases fail with auth, quota, model selection, or project errors.
- Result files show invalid JSON.
- Stdout files are empty for successful exit-code-0 cases.
- ACP probe artifacts are missing or show initialization/authentication failure.
- The capture used an unintended output directory, account, model list, or
  timeout.

If investigation is required, do not delete the output directory. Record the
exact command line, environment overrides, Gemini CLI version, mitmproxy
version, and the result-code summary.

## Handoff Checklist

Include the following in the operation handoff:

- Capture start and end time.
- Hostname and operator.
- `gemini --version`.
- `mitmdump --version`.
- Output directory.
- Environment overrides.
- Result count and exit-code distribution.
- Model distribution.
- Whether `flows.mitm` is present and non-empty.
- List of accepted failures with case prefixes.
- List of failures needing follow-up.
