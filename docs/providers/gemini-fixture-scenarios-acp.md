# Gemini ACP Fixture Scenario Matrix

This document defines the target fixture matrix for future Gemini CLI `--acp`
JSON-RPC captures. It is a capture plan only; do not run the long capture as
part of editing this document.

The capture output should be newline-delimited JSON-RPC logs named
`rpc.ndjson`, with each line shaped as:

```json
{"direction":"send","message":{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}}
```

## Coverage Goals

The fixture set should cover:

- Agent methods: `initialize`, `authenticate`, `session/new`, `session/list`,
  `session/load`, `session/fork`, `session/resume`, `session/set_model`,
  `session/set_mode`, `session/set_config_option`, `session/prompt`, and
  `session/cancel`.
- Prompt blocks: embedded `text`, `image`, `audio`, and `resource` blocks.
- MCP declarations: local stdio, HTTP streamable, and SSE servers.
- Provider callbacks: `session/update`, `session/request_permission`,
  `fs/read_text_file`, `fs/write_text_file`, `terminal/create`,
  `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`, and
  `terminal/release`.
- Negative behavior: invalid params, unknown session IDs, denied permissions,
  client callback errors, agent errors, and client-side timeouts.

## Matrix

| ID | Scenario | Setup | JSON-RPC flow | Expected fixture evidence |
| --- | --- | --- | --- | --- |
| ACP-001 | Protocol handshake | Start `gemini --acp` in an empty fixture workspace. | `initialize` with `protocolVersion: 1` and minimal client capabilities. | Response includes agent capabilities, server info, supported prompt blocks, and MCP transport capability flags. |
| ACP-002 | Auth status | Valid Gemini OAuth profile. | `initialize`, `authenticate`. | Captures whether auth succeeds silently, returns status metadata, or emits auth-related callback requests. |
| ACP-003 | Auth failure | Run with isolated `HOME` or invalid `.gemini` credential dir. | `initialize`, `authenticate`. | JSON-RPC error or structured unauthenticated result; stderr captured separately. |
| ACP-004 | New session, no MCP | Controlled workspace with `GEMINI.md`, no MCP declarations. | `initialize`, `session/new`. | `sessionId`, cwd handling, workspace metadata, and any startup `session/update` notifications. |
| ACP-005 | New session with stdio MCP | Local fixture MCP server command and args. | `initialize`, `session/new` with `mcpServers:[{name,command,args,env}]`. | MCP server declaration is accepted; later prompt can trigger tool discovery/use. |
| ACP-006 | New session with HTTP MCP | Local HTTP MCP fixture server on loopback. | `session/new` with an HTTP MCP declaration. | Accepted or rejected declaration shape, including any transport-specific validation errors. |
| ACP-007 | New session with SSE MCP | Local SSE MCP fixture server on loopback. | `session/new` with an SSE MCP declaration. | Accepted or rejected declaration shape and whether initialize response advertised SSE support. |
| ACP-008 | List sessions | At least two created sessions in one ACP process. | `session/new`, `session/new`, `session/list`. | List result shape, ordering, fields, and empty-list behavior if run before creation. |
| ACP-009 | Load session | Create a session, prompt once, then load by returned ID or persisted handle. | `session/new`, `session/prompt`, `session/load`. | Load result fields and whether prompt history is reflected in subsequent updates. |
| ACP-010 | Fork session | Create a session with prior prompt history. | `session/new`, `session/prompt`, `session/fork`. | New forked session ID, parent linkage if present, and whether state diverges after next prompt. |
| ACP-011 | Resume session | Create or load a session that can be resumed. | `session/resume` with known session ID. | Resume result shape and any replayed `session/update` events. |
| ACP-012 | Set model accepted | Use a known valid model alias. | `session/new`, `session/set_model`. | Accepted result shape and subsequent prompt metadata showing selected model if emitted. |
| ACP-013 | Set model invalid | Use a deliberately invalid model ID. | `session/new`, `session/set_model`. | JSON-RPC error code/message or structured validation failure. |
| ACP-014 | Set mode accepted | Try common mode IDs such as `default`, `plan`, or observed Gemini modes. | `session/new`, `session/set_mode`. | Accepted mode result or discoverable invalid-mode error. |
| ACP-015 | Set mode invalid | Use `modeId:"pangaea-invalid-mode"`. | `session/new`, `session/set_mode`. | JSON-RPC error and no subsequent mode-dependent behavior change. |
| ACP-016 | Set config option | Toggle a safe option such as approval, telemetry, or tool policy if accepted. | `session/new`, `session/set_config_option`. | Exact option name/value shape, result semantics, and validation behavior. |
| ACP-017 | Text prompt | Simple deterministic text prompt. | `session/prompt` with `[{type:"text",text:"Reply with exactly ACP_OK."}]`. | Streaming `session/update` chunks and final completion result. |
| ACP-018 | Resource prompt | Attach a text/markdown file as an embedded `resource` block. | `session/prompt` with text plus `resource:{uri,mimeType,text}`. | Prompt is accepted and model answer references the resource content. |
| ACP-019 | Image prompt | Attach tiny PNG and realistic screenshot PNG blocks. | `session/prompt` with `image:{data,mimeType}`. | Image block accepted, update stream remains valid, response references visual content. |
| ACP-020 | Audio prompt | Attach a short WAV or MP3 block. | `session/prompt` with `audio:{data,mimeType}` if supported by observed schema. | Accepted multimodal audio behavior or explicit unsupported-block error. |
| ACP-021 | Mixed prompt order | Combine text, resource, image, audio, then text. | One `session/prompt` with all embedded block types. | Captures ordering semantics and annotations such as `audience` and `priority`. |
| ACP-022 | MCP tool request | Prompt asks to call fixture MCP `fixture_echo`. | `session/prompt`; client responds to any permission callback. | Tool-related `session/update` events, permission request if present, final tool result text. |
| ACP-023 | Permission allow once | Prompt requests a file write or shell command in plan/approval mode. | `session/prompt`; reply to `session/request_permission` with selected allow option. | Permission request shape, option IDs, allowed action updates, and final result. |
| ACP-024 | Permission deny | Same as ACP-023 but deny. | `session/request_permission` response selects deny option. | Denial propagation into model output and absence of side-effect callbacks. |
| ACP-025 | Filesystem read callback | Prompt asks to inspect a known workspace file. | `session/prompt`; handle `fs/read_text_file` with fixture content. | File read params, returned content shape, and response using that content. |
| ACP-026 | Filesystem write callback | Prompt asks to create a disposable fixture file. | `session/prompt`; handle `fs/write_text_file` with success. | Write params include path/content; updates reflect tool execution. |
| ACP-027 | Filesystem callback error | Same as ACP-025 but client returns JSON-RPC error. | `fs/read_text_file` response with error object. | Agent handles callback failure without corrupting the stream. |
| ACP-028 | Terminal lifecycle | Prompt asks to run `printf pangaea-terminal-ok`. | `terminal/create`, `terminal/output`, `terminal/wait_for_exit`, `terminal/release`. | Full terminal callback lifecycle, command params, output payloads, and exit status. |
| ACP-029 | Terminal kill | Prompt asks to run a long command, then client cancels or kills. | `terminal/create`, `terminal/kill`, `terminal/release`. | Kill request/response shape and final interrupted status. |
| ACP-030 | Prompt cancel | Send a long-running prompt and cancel quickly. | `session/prompt`, then `session/cancel` for same session or prompt ID if provided. | Cancel result, any late updates, and final prompt outcome semantics. |
| ACP-031 | Unknown session | Use a fabricated session ID. | `session/load`, `session/resume`, `session/prompt`, or `session/cancel`. | Stable not-found errors and method-specific error messages. |
| ACP-032 | Malformed params | Send valid JSON-RPC with missing required params. | Example: `session/prompt` without `sessionId`. | JSON-RPC invalid-params error and process remains alive for the next valid request. |
| ACP-033 | Unknown method | Send a method outside the schema. | `pangaea/unknown`. | JSON-RPC `Method not found` behavior and process liveness. |
| ACP-034 | Client callback timeout | Do not answer a provider callback. | Trigger permission, fs, or terminal callback and hold response past timeout. | Agent timeout behavior, stderr, and whether subsequent requests are possible. |
| ACP-035 | Client callback invalid result | Reply to callback with malformed result shape. | Trigger callback and return wrong object fields. | Agent-side validation behavior or recovery updates. |
| ACP-036 | Concurrent prompts | Send two prompts before the first completes, if accepted by client harness. | Two `session/prompt` requests for same session. | Serialization, rejection, or interleaved update semantics. |

## Prompt Block Shapes

Use these as canonical candidate payloads. Adjust only when captures show Gemini
CLI expects different field names.

```json
[
  {"type":"text","text":"Reply with exactly ACP_OK."},
  {
    "type":"resource",
    "resource":{
      "uri":"file:fixtures/sample.md",
      "mimeType":"text/markdown",
      "text":"# Fixture Notes\n..."
    },
    "annotations":{"audience":["assistant"],"priority":0.8}
  },
  {
    "type":"image",
    "data":"<base64>",
    "mimeType":"image/png",
    "annotations":{"audience":["assistant"],"priority":0.5}
  },
  {
    "type":"audio",
    "data":"<base64>",
    "mimeType":"audio/wav",
    "annotations":{"audience":["assistant"],"priority":0.5}
  }
]
```

## MCP Declaration Candidates

Capture each declaration independently so unsupported transport failures do not
mask unrelated behavior.

```json
[
  {
    "name":"pangaea-stdio",
    "command":"node",
    "args":["./fixture/mcp-server.mjs"],
    "env":[]
  },
  {
    "name":"pangaea-http",
    "url":"http://127.0.0.1:18765/mcp",
    "transport":"http"
  },
  {
    "name":"pangaea-sse",
    "url":"http://127.0.0.1:18766/sse",
    "transport":"sse"
  }
]
```

## Validation Checklist

Validate `rpc.ndjson` before promoting a fixture:

1. Every non-empty line parses as JSON.
2. Every `message` has `jsonrpc:"2.0"`.
3. Every request with an `id` has exactly one response with the same `id`.
4. Notifications have `method` and no `id`.
5. Responses have exactly one of `result` or `error`.
6. `direction` is only `send` or `recv`.
7. Agent method calls appear in a valid lifecycle order: `initialize` before
   session methods, `session/new` or `session/load` before session-scoped
   operations.
8. Client callbacks are answered unless the fixture intentionally tests timeout.
9. Timeout fixtures include wall-clock metadata or harness summary explaining
   which request was intentionally left pending.
10. Error fixtures prove the process remains usable by sending a final simple
    valid request after the error when possible.
11. Multimodal fixtures include stable hashes and MIME types for binary blocks
    outside `rpc.ndjson` so reviewers can verify base64 payload provenance.
12. `stderr.log` and harness summaries are kept with the same scenario ID as
    `rpc.ndjson`.

Minimal line parser:

```bash
node -e '
const fs = require("fs");
const path = process.argv[1];
const ids = new Map();
let lineNo = 0;
for (const line of fs.readFileSync(path, "utf8").split(/\n/)) {
  if (!line.trim()) continue;
  lineNo++;
  const entry = JSON.parse(line);
  if (!["send", "recv"].includes(entry.direction)) throw new Error(`bad direction at ${lineNo}`);
  const msg = entry.message;
  if (!msg || msg.jsonrpc !== "2.0") throw new Error(`bad jsonrpc at ${lineNo}`);
  if (msg.method && msg.id !== undefined) ids.set(`${entry.direction}:${msg.id}`, lineNo);
  if (!msg.method && msg.id !== undefined && msg.result === undefined && msg.error === undefined) {
    throw new Error(`response without result/error at ${lineNo}`);
  }
}
console.log(`validated ${lineNo} rpc lines`);
' .tmp/gemini-fixtures/acp/rpc.ndjson
```

## Capture Notes

- Keep each scenario in its own directory:
  `.tmp/gemini-fixtures/acp/<scenario-id>/rpc.ndjson`.
- Record `gemini --version`, model ID, cwd, environment overrides, and fixture
  asset hashes in a sibling `summary.json`.
- Use short deterministic prompts for protocol shape fixtures and reserve
  longer prompts for streaming, cancellation, terminal, and timeout behavior.
- Mark expected-failure captures explicitly. Unsupported audio, HTTP MCP, or
  SSE MCP behavior is still valuable if the JSON-RPC error shape is stable.
