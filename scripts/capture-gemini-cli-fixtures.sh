#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out_dir="${PANGAEA_GEMINI_FIXTURE_DIR:-${repo_root}/.tmp/gemini-fixtures}"
limit="${PANGAEA_GEMINI_FIXTURE_LIMIT:-300}"
start_index="${PANGAEA_GEMINI_FIXTURE_START_INDEX:-1}"
models_csv="${PANGAEA_GEMINI_FIXTURE_MODELS:-auto-gemini-3,auto-gemini-2.5,gemini-3.1-pro-preview,gemini-3-flash-preview,gemini-3.1-flash-lite-preview,gemini-2.5-pro,gemini-2.5-flash,gemini-2.5-flash-lite}"
mitm="${PANGAEA_GEMINI_FIXTURE_MITM:-auto}"
case_timeout="${PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT:-180}"
mkdir -p "${out_dir}"
out_dir="$(cd "${out_dir}" && pwd)"
mkdir -p "${out_dir}/cli" "${out_dir}/acp" "${out_dir}/http"
fixture_workspace="${out_dir}/workspace"
mkdir -p "${fixture_workspace}/.gemini" "${fixture_workspace}/fixtures"
progress_log="${out_dir}/progress.ndjson"

case "${limit}" in
  ''|*[!0-9]*) echo "PANGAEA_GEMINI_FIXTURE_LIMIT must be an integer" >&2; exit 2 ;;
esac
case "${start_index}" in
  ''|*[!0-9]*) echo "PANGAEA_GEMINI_FIXTURE_START_INDEX must be an integer" >&2; exit 2 ;;
esac
if [ "${start_index}" -lt 1 ] || [ "${start_index}" -gt "${limit}" ]; then
  echo "PANGAEA_GEMINI_FIXTURE_START_INDEX must be between 1 and ${limit}" >&2
  exit 2
fi
case "${case_timeout}" in
  ''|*[!0-9]*) echo "PANGAEA_GEMINI_FIXTURE_CASE_TIMEOUT must be an integer number of seconds" >&2; exit 2 ;;
esac

export TERM="${TERM:-xterm-256color}"
if [ "${TERM}" = "dumb" ]; then export TERM=xterm-256color; fi
export COLORTERM="${COLORTERM:-truecolor}"
export FORCE_COLOR="${FORCE_COLOR:-1}"
unset NO_COLOR

cat >"${fixture_workspace}/GEMINI.md" <<'EOF'
# Pangaea Gemini Fixture System Memory

When a prompt asks about this workspace, identify it as a controlled Pangaea
Gemini CLI fixture workspace. Keep answers concise unless the user explicitly
asks for detail.
EOF

cat >"${fixture_workspace}/fixtures/sample.go" <<'EOF'
package main

import "fmt"

func main() {
	fmt.Println("hello from pangaea fixture")
}
EOF

cat >"${fixture_workspace}/fixtures/sample.md" <<'EOF'
# Fixture Notes

1. Streaming responses should arrive incrementally.
2. Buffered responses should arrive as one complete object.

```go
fmt.Println("markdown fixture")
```
EOF

cat >"${fixture_workspace}/fixtures/data.json" <<'EOF'
{"service":"gemini","adapter":"cli-adapter","direct_http_name":"direct-http","count":3}
EOF

cat >"${fixture_workspace}/fixtures/policy.toml" <<'EOF'
[tools]
allowed = ["read_file", "list_directory"]
EOF

cat >"${fixture_workspace}/fixtures/long-context.txt" <<'EOF'
Pangaea router coordinates isolated CLI providers, auth refresh, usage probes,
and OpenAI/Anthropic/Gemini compatibility routing. This text exists to exercise
larger prompt payloads and context accounting in fixture captures.
EOF

cat >"${fixture_workspace}/fixtures/table.csv" <<'EOF'
service,mode,stream
gemini,cli-adapter,true
gemini,direct-http,false
codex,app-server,true
EOF

base64 -d >"${fixture_workspace}/fixtures/tiny.png" <<'EOF'
iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=
EOF

cat >"${fixture_workspace}/mcp-server.mjs" <<'EOF'
#!/usr/bin/env node
import { createInterface } from "node:readline";

const rl = createInterface({ input: process.stdin });

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

rl.on("line", (line) => {
  if (!line.trim()) return;
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    return;
  }
  const id = request.id;
  if (request.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id,
      result: {
        protocolVersion: request.params?.protocolVersion || "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "pangaea-fixture-mcp", version: "0.0.0" },
      },
    });
    return;
  }
  if (request.method === "tools/list") {
    send({
      jsonrpc: "2.0",
      id,
      result: {
        tools: [{
          name: "fixture_echo",
          description: "Echo fixture input for Pangaea capture tests.",
          inputSchema: {
            type: "object",
            properties: { text: { type: "string" } },
            required: ["text"],
          },
        }],
      },
    });
    return;
  }
  if (request.method === "tools/call") {
    const text = request.params?.arguments?.text || "";
    send({
      jsonrpc: "2.0",
      id,
      result: {
        content: [{ type: "text", text: `fixture_echo:${text}` }],
        isError: false,
      },
    });
    return;
  }
  if (id !== undefined) {
    send({ jsonrpc: "2.0", id, result: {} });
  }
});
EOF
chmod 0755 "${fixture_workspace}/mcp-server.mjs"

cat >"${fixture_workspace}/.gemini/settings.json" <<EOF
{
  "mcpServers": {
    "pangaea-fixture": {
      "command": "node",
      "args": ["${fixture_workspace}/mcp-server.mjs"],
      "env": {}
    }
  },
  "mcp": {
    "allowed": ["pangaea-fixture"]
  }
}
EOF

mitm_pid=""
cleanup() {
  if [ -n "${mitm_pid}" ] && kill -0 "${mitm_pid}" 2>/dev/null; then
    kill "${mitm_pid}" 2>/dev/null || true
    wait "${mitm_pid}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

start_mitm() {
  if ! command -v mitmdump >/dev/null 2>&1; then
  if [ "${mitm}" = "1" ]; then
      echo "mitmdump is required when PANGAEA_GEMINI_FIXTURE_MITM=1" >&2
      exit 2
    fi
    return
  fi
  local port="${PANGAEA_GEMINI_MITM_PORT:-18089}"
  local flow_file="${out_dir}/http/flows.mitm"
  local log_file="${out_dir}/http/mitmdump.log"
  if [ "${start_index}" -gt 1 ]; then
    flow_file="${out_dir}/http/flows-${start_index}-${limit}.mitm"
    log_file="${out_dir}/http/mitmdump-${start_index}-${limit}.log"
  fi
  mitmdump --listen-host 127.0.0.1 --listen-port "${port}" -w "${flow_file}" >"${log_file}" 2>&1 &
  mitm_pid="$!"
  sleep 2
  export HTTPS_PROXY="http://127.0.0.1:${port}"
  export HTTP_PROXY="http://127.0.0.1:${port}"
  export ALL_PROXY="http://127.0.0.1:${port}"
  local ca="${HOME}/.mitmproxy/mitmproxy-ca-cert.pem"
  if [ -s "${ca}" ]; then
    export NODE_EXTRA_CA_CERTS="${ca}"
  else
    echo "mitmproxy CA not found at ${ca}; Node may reject intercepted TLS" >&2
  fi
}

if [ "${mitm}" = "1" ] || [ "${mitm}" = "auto" ]; then
  start_mitm
fi

IFS=',' read -r -a models <<<"${models_csv}"
prompts=(
  "Reply with exactly OK."
  "Write a Markdown answer with headings, ordered lists, and a fenced Go code block for hello world."
  "Explain Go error handling in Korean in three short bullets."
  "Create a JSON object with keys language, example, caveat."
  "Read the current workspace conceptually and say which file you would inspect first; do not modify files."
  "Use markdown table format to compare streaming and buffered responses."
  "Answer with a short system-design note about token refresh."
  "If a tool call would be useful, explain which tool and why without executing it."
  "Mention MCP server integration considerations in two sentences."
  "Summarize how image input should be validated before sending to a model."
  "Use the injected Go file @{fixtures/sample.go} and summarize what it prints."
  "Use the injected Markdown file @{fixtures/sample.md} and preserve the ordered list numbering."
  "Use the injected JSON file @{fixtures/data.json} and report adapter names."
  "Inspect this image @{fixtures/tiny.png} and describe it in one sentence."
  "Use the pangaea-fixture MCP fixture_echo tool with text 'mcp-ok', then summarize the result."
  "Use the workspace GEMINI.md memory and say the workspace label."
  "Return a compact YAML document describing cli-adapter and direct-http."
  "Write a Python hello world and include exactly one fenced python code block."
  "Write a TypeScript fetch example and include one warning about retries."
  "Use @{fixtures/table.csv}; render it as a Markdown table without changing column order."
  "Use @{fixtures/long-context.txt}; summarize it in one sentence and report the phrase 'context accounting'."
  "Use @{fixtures/policy.toml}; explain whether it is permissive or restrictive."
  "Ask for a read-only plan to inspect this workspace; do not request writes."
  "Explain how a router should retry 429 and 503 in four numbered steps."
  "Produce Markdown with a heading '### 1. Result' and do not turn the heading into a list."
  "Produce an ordered list with a fenced Go block between items 2 and 3; preserve numbering."
  "Give a terse JSON object only, with keys model, stream, buffered."
  "Explain Gemini CLI token refresh observations and mention oauth_creds.json."
  "Compare MCP stdio, HTTP, and SSE transports in a compact table."
  "If file access is required, identify the exact file path you would read first."
  "Use @{fixtures/sample.go} and @{fixtures/data.json}; combine their facts in one paragraph."
  "Use @{fixtures/sample.md} and @{fixtures/table.csv}; preserve Markdown and CSV distinctions."
  "Inspect @{fixtures/tiny.png}; say if it appears simple or complex."
  "Call pangaea-fixture fixture_echo with text 'agentic-001' if tools are available; otherwise explain why not."
  "Generate a response that starts with STREAM_TEST_BEGIN and ends with STREAM_TEST_END."
  "Generate a response that starts with BUFFER_TEST_BEGIN and ends with BUFFER_TEST_END."
  "Describe how to convert OpenAI chat messages into Gemini content parts."
  "Describe how to convert Gemini streamed chunks into OpenAI SSE chat chunks."
  "Explain what should be validated before accepting an uploaded image."
  "Explain what should be validated before accepting a text file attachment."
  "Mention the difference between provider id and provider instance id."
  "Mention why host_name must be the host-side name, not the container hostname."
  "Draft a short auth update event history with three events."
  "Draft a usage quota row with remaining percent and reset time fields."
  "List three reasons a provider should enter auth-updating state."
  "Explain why a request should route away from auth-updating providers."
  "Write a short Korean answer about Go interfaces."
  "Write a short Korean answer about Docker init containers."
  "Write a short Korean answer about Kubernetes persistent volumes."
  "Answer in English with one paragraph about JSON-RPC newline framing."
  "Answer in Korean with one paragraph about SSE framing."
  "Return only the string EXACT_LITERAL_12345."
  "Return exactly two lines: FIRST_LINE and SECOND_LINE."
  "Write a Markdown blockquote followed by a Go code block."
  "Write a small diff-style patch snippet without claiming you edited files."
  "Explain how cancellation should be represented in ACP."
  "Explain how session/fork differs from session/load in ACP."
  "Explain why session/close may be absent even if schema mentions it."
  "Summarize the current prompt in less than 20 words."
  "Use stdin if present and report whether stdin-content-fixture=true is present."
  "Use @{fixtures/long-context.txt}; output five keywords only."
  "Use @{fixtures/sample.go}; explain the package, import, and main function."
  "Use @{fixtures/data.json}; mention cli-adapter and direct-http exactly once each."
  "Make a checklist for validating captured fixture artifacts."
  "Give a compact risk note for running 300 capture requests against quota."
  "Explain how mitmproxy CA affects Node HTTPS requests."
  "Explain why TERM and COLORTERM matter for CLI subprocesses."
  "Explain why ripgrep availability changes Gemini CLI behavior."
  "Describe a fallback when stream-json is unavailable."
  "Describe a fallback when ACP prompt image blocks fail."
)

mode_cycle=(plan default auto_edit yolo)

run_cli_case() {
  local index="$1"
  local model="$2"
  local prompt="$3"
  local case_slot="$4"
  local prefix
  prefix="$(printf "%s/cli/%03d_%s" "${out_dir}" "${index}" "${model//[^A-Za-z0-9_.-]/_}")"
  local output_format="stream-json"
  local approval_mode="${mode_cycle[$((case_slot % ${#mode_cycle[@]}))]}"
  local stdin_payload=""
  local -a args
  local started_at ended_at duration_ms
  started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local start_epoch_ms
  start_epoch_ms="$(date +%s%3N)"
  if [ $((case_slot % 5)) -eq 0 ]; then
    output_format="json"
  fi
  if [ $((index % 9)) -eq 0 ]; then
    stdin_payload=$'Additional stdin context: stdin-content-fixture=true\nContinuation marker: pangaea-fixture-continuation\n'
  fi
  args=(
    -p "${prompt}"
    --skip-trust
    --approval-mode "${approval_mode}"
    --output-format "${output_format}"
    --model "${model}"
    --allowed-mcp-server-names pangaea-fixture
  )
  if [ $((index % 13)) -eq 0 ]; then
    args+=(--include-directories "${fixture_workspace}/fixtures")
  fi
  if [ $((index % 17)) -eq 0 ]; then
    args+=(--policy "${fixture_workspace}/fixtures/policy.toml")
  fi
  local -a runner
  if command -v timeout >/dev/null 2>&1; then
    runner=(timeout --preserve-status "${case_timeout}s")
  else
    runner=()
  fi
  node -e 'console.log(JSON.stringify({index:Number(process.argv[1]), model:process.argv[2], prompt:process.argv[3], cwd:process.argv[4], output_format:process.argv[5], approval_mode:process.argv[6], stdin_bytes:Number(process.argv[7]), started_at:process.argv[8]}))' \
    "${index}" "${model}" "${prompt}" "${fixture_workspace}" "${output_format}" "${approval_mode}" "${#stdin_payload}" "${started_at}" >"${prefix}.request.json"
  node -e 'console.log(JSON.stringify({event:"start", index:Number(process.argv[1]), model:process.argv[2], output_format:process.argv[3], approval_mode:process.argv[4], started_at:process.argv[5]}))' \
    "${index}" "${model}" "${output_format}" "${approval_mode}" "${started_at}" >>"${progress_log}"
  printf '[%s] start %03d/%03d model=%s format=%s mode=%s\n' "${started_at}" "${index}" "${limit}" "${model}" "${output_format}" "${approval_mode}"
  printf '%s\n' "${args[@]}" >"${prefix}.argv.txt"
  set +e
  if [ -n "${stdin_payload}" ]; then
    (cd "${fixture_workspace}" && printf '%s' "${stdin_payload}" | "${runner[@]}" gemini "${args[@]}") >"${prefix}.stdout.ndjson" 2>"${prefix}.stderr.log"
  else
    (cd "${fixture_workspace}" && "${runner[@]}" gemini "${args[@]}") >"${prefix}.stdout.ndjson" 2>"${prefix}.stderr.log"
  fi
  local rc="$?"
  set -e
  ended_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  duration_ms="$(($(date +%s%3N) - start_epoch_ms))"
  node -e 'console.log(JSON.stringify({index:Number(process.argv[1]), model:process.argv[2], exit_code:Number(process.argv[3]), output_format:process.argv[4], approval_mode:process.argv[5], started_at:process.argv[6], ended_at:process.argv[7], duration_ms:Number(process.argv[8])}))' \
    "${index}" "${model}" "${rc}" "${output_format}" "${approval_mode}" "${started_at}" "${ended_at}" "${duration_ms}" >"${prefix}.result.json"
  node -e 'console.log(JSON.stringify({event:"end", index:Number(process.argv[1]), model:process.argv[2], exit_code:Number(process.argv[3]), output_format:process.argv[4], approval_mode:process.argv[5], started_at:process.argv[6], ended_at:process.argv[7], duration_ms:Number(process.argv[8])}))' \
    "${index}" "${model}" "${rc}" "${output_format}" "${approval_mode}" "${started_at}" "${ended_at}" "${duration_ms}" >>"${progress_log}"
  printf '[%s] end   %03d/%03d model=%s rc=%s duration_ms=%s\n' "${ended_at}" "${index}" "${limit}" "${model}" "${rc}" "${duration_ms}"
}

if ! command -v gemini >/dev/null 2>&1; then
  echo "gemini CLI is not available in PATH" >&2
  exit 2
fi

for ((i=start_index; i<=limit; i++)); do
  model="${models[$(((i - 1) % ${#models[@]}))]}"
  prompt="${prompts[$(((i - 1) % ${#prompts[@]}))]}"
  run_cli_case "${i}" "${model}" "${prompt}" "$(((i - 1) % ${#prompts[@]}))"
done

PANGAEA_GEMINI_FIXTURE_DIR="${out_dir}/acp" \
PANGAEA_GEMINI_ACP_MODEL="${models[0]}" \
PANGAEA_GEMINI_ACP_PROMPT="Use the attached text resource and image if present. Reply with exactly ACP_OK plus one word summary." \
PANGAEA_GEMINI_ACP_CWD="${fixture_workspace}" \
PANGAEA_GEMINI_ACP_RESOURCE_FILE="${fixture_workspace}/fixtures/sample.md" \
PANGAEA_GEMINI_ACP_IMAGE_FILE="${fixture_workspace}/fixtures/tiny.png" \
PANGAEA_GEMINI_ACP_MCP_COMMAND="node" \
PANGAEA_GEMINI_ACP_MCP_ARGS="${fixture_workspace}/mcp-server.mjs" \
node "${repo_root}/scripts/gemini-acp-probe.mjs"

cat >"${out_dir}/manifest.json" <<EOF
{
  "target": "gemini-cli",
  "adapter": "cli-adapter",
  "direct_http_name": "direct-http",
  "limit": ${limit},
  "start_index": ${start_index},
  "models": "$(printf '%s' "${models_csv}" | sed 's/"/\\"/g')",
  "mitm": "$(printf '%s' "${mitm}" | sed 's/"/\\"/g')",
  "case_timeout_seconds": ${case_timeout}
}
EOF

echo "Gemini CLI fixtures written to ${out_dir}"
