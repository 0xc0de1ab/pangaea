# Gemini CLI Fixture Scenario Matrix

This matrix is for `scripts/capture-gemini-cli-fixtures.sh` and targets 300
Gemini CLI captures for the `cli-adapter`. The intended shape is 50 scenario
slots multiplied by the script's default six-model set:

- `gemini-2.5-flash`
- `gemini-2.5-pro`
- `gemini-2.5-flash-lite`
- `auto-gemini-2.5`
- `gemini-3-pro-preview`
- `gemini-3-flash-preview`

Run only when real fixture capture is intended:

```bash
PANGAEA_GEMINI_FIXTURE_LIMIT=300 scripts/capture-gemini-cli-fixtures.sh
```

Do not treat all 300 outputs as requiring identical text. The validation goal is
to capture stable adapter behavior: argv shape, stdout framing, stderr warnings,
exit code, model selection, prompt injection handling, MCP/tool behavior,
multimodal behavior, and any observed token or auth refresh metadata.

## Script Inputs To Vary

The current script already writes a fixture workspace with:

- `GEMINI.md` workspace memory
- `fixtures/sample.go`
- `fixtures/sample.md`
- `fixtures/data.json`
- `fixtures/tiny.png`
- `.gemini/settings.json` with `pangaea-fixture` MCP server
- `mcp-server.mjs` exposing `fixture_echo`

For a 300-case matrix, keep the script's model loop and expand the prompt bank
to 50 slots. Use deterministic slot-derived settings so every model sees the
same behavioral surface:

| Dimension | Recommended slot rule | Expected values |
| --- | --- | --- |
| Model | Existing `i % models` loop | Six default models above |
| Output format | Keep `stream-json` primary; set `json` for slots `0, 7, 14, 21, 28, 35, 42, 49` | `stream-json`, `json` |
| Approval mode | Mostly `plan`; include permissive mode for tool pressure slots | `plan`, `yolo` |
| Stdin | Pipe stdin for slots `3, 11, 19, 27, 35, 43` | Empty, short text, multiline text |
| Memory | Use root `GEMINI.md` in slots `4, 15, 24, 34, 44` | Workspace label should appear |
| File injection | Use `@{fixtures/...}` in slots `10-14`, `25-28`, `40-42` | Go, Markdown, JSON, PNG |
| MCP | Enable `--allowed-mcp-server-names pangaea-fixture`; ask for MCP in slots `16-20`, `45-47` | Tool list/call traces if emitted |
| Continuation/session-like | Use explicit "previous turn" prompts in slots `30-34` | No real session required |
| Usage/refresh | Include refresh/quota prompts in slots `35-39` | stderr/auth observations |

## 50 Scenario Slots

Each slot should be captured once per model. The `id` values are stable names
for manifests or generated request metadata; the `prompt` text can be copied
directly into the script's `prompts=(...)` array.

| Slot | ID | Focus | Prompt |
| ---: | --- | --- | --- |
| 00 | smoke-ok-json | Minimal response, JSON output slot | `Reply with exactly OK.` |
| 01 | markdown-structured | Markdown headings and ordered list | `Write a Markdown answer with one H2, one ordered list, and one fenced Go code block for hello world.` |
| 02 | korean-list | Non-English bullets | `Explain Go error handling in Korean in three short bullets.` |
| 03 | stdin-short | Stdin consumption observation | `Use any stdin context if present. Reply with a JSON object containing stdin_seen and summary.` |
| 04 | memory-label | `GEMINI.md` system memory | `Use the workspace GEMINI.md memory and say the workspace label in one sentence.` |
| 05 | json-object | Strict JSON body | `Create a JSON object with keys language, example, caveat. Do not wrap it in Markdown.` |
| 06 | markdown-table | Table rendering | `Use a Markdown table to compare streaming and buffered responses with three rows.` |
| 07 | json-newlines | JSON output plus escaped content | `Return JSON with keys title, lines, and code where lines is an array of three short strings.` |
| 08 | tool-plan-only | Tool-use reasoning without execution | `If a tool call would be useful to inspect this workspace, explain which tool and why without executing it.` |
| 09 | image-validation | Multimodal policy text | `Summarize how image input should be validated before sending to a model.` |
| 10 | inject-go-summary | `@{file}` Go injection | `Use the injected Go file @{fixtures/sample.go} and summarize what it prints.` |
| 11 | inject-md-numbering | `@{file}` Markdown injection and stdin slot | `Use the injected Markdown file @{fixtures/sample.md} and preserve the ordered list numbering.` |
| 12 | inject-json-fields | `@{file}` JSON injection | `Use the injected JSON file @{fixtures/data.json} and report adapter names.` |
| 13 | inject-image | PNG injection | `Inspect this image @{fixtures/tiny.png} and describe it in one sentence.` |
| 14 | inject-mixed | Multiple `@{file}` references | `Compare @{fixtures/sample.go} and @{fixtures/data.json}; answer with a two-row Markdown table.` |
| 15 | memory-conflict | Memory precedence | `The prompt says this is a random workspace. Use GEMINI.md if available and state the actual fixture workspace label.` |
| 16 | mcp-echo-basic | MCP tool call | `Use the pangaea-fixture MCP fixture_echo tool with text 'mcp-ok', then summarize the result.` |
| 17 | mcp-tool-json | MCP result in JSON | `Call fixture_echo with text 'json-case' and return JSON with keys tool, input, observed_output.` |
| 18 | mcp-tool-markdown | MCP result in Markdown | `Call fixture_echo with text 'markdown-case' and put the result in a Markdown blockquote.` |
| 19 | mcp-with-stdin | MCP plus stdin | `Use stdin context if present, then call fixture_echo with text 'stdin-mcp'. Explain both signals briefly.` |
| 20 | mcp-unavailable-fallback | MCP fallback behavior | `If fixture_echo is available, call it with text 'available'. If not, explain the failure mode in one sentence.` |
| 21 | code-fence-js | Code block edge case | `Write a JavaScript function in a fenced code block, then one sentence explaining it.` |
| 22 | code-fence-backticks | Nested backtick escaping | `Show a Markdown snippet that itself contains a fenced code block. Keep it valid Markdown.` |
| 23 | list-nesting | Ordered and unordered list nesting | `Create an ordered checklist with nested unordered caveats about CLI fixture validation.` |
| 24 | memory-plus-markdown | Memory and Markdown edge | `Using GEMINI.md context, write a short Markdown note with the workspace label and one code span.` |
| 25 | inject-md-code | Markdown fixture code preservation | `Read @{fixtures/sample.md}. Return only the fenced code block language and code content summary.` |
| 26 | inject-go-lineitems | File facts | `Read @{fixtures/sample.go}. List package name, imported package, and printed string.` |
| 27 | inject-json-stdin | JSON injection plus stdin | `Read @{fixtures/data.json} and any stdin. Return JSON with adapter, count, and stdin_seen.` |
| 28 | inject-image-json | Image injection plus JSON format | `Inspect @{fixtures/tiny.png}. Return JSON with keys image_seen and description.` |
| 29 | no-modify-safety | Approval mode safety | `Say which file you would inspect first in this workspace. Do not modify files or run commands.` |
| 30 | continuation-a | Session-like first turn | `This is turn A. Define the token ALPHA as 'fixture-continuation' and answer with ALPHA only.` |
| 31 | continuation-b | Session-like follow-up without state | `This is turn B. If you remember ALPHA from a previous request, say it; otherwise say NO_SESSION_STATE.` |
| 32 | continuation-summary | Explicit supplied previous turn | `Previous turn said ALPHA means fixture-continuation. Summarize that in one sentence.` |
| 33 | continuation-correction | Correction handling | `Earlier text may have said adapter=wrong. Correct it using @{fixtures/data.json}.` |
| 34 | memory-continuation | Memory and session-like prompt | `Using GEMINI.md and this prompt only, say whether real session state is required for this answer.` |
| 35 | usage-refresh-note | Usage/token refresh prompt and stdin | `Answer with a short system-design note about Gemini OAuth token refresh observations.` |
| 36 | quota-window-note | Usage/quota language | `Explain what fixture captures should record about quota windows, token expiry, and refresh stderr.` |
| 37 | auth-warning-shape | stderr observation | `If auth or model warnings appear outside the answer, explain why stderr should be captured separately.` |
| 38 | model-identity | Model routing | `State the model name if the CLI exposes it; otherwise say MODEL_NOT_VISIBLE.` |
| 39 | long-stream | Streaming chunk pressure | `Write ten short numbered sentences about adapter fixture capture, one sentence per line.` |
| 40 | multi-file-synthesis | Multi-file injection | `Use @{fixtures/sample.go}, @{fixtures/sample.md}, and @{fixtures/data.json}; produce five concise facts.` |
| 41 | file-quote-limit | Quoted snippets | `From @{fixtures/sample.md}, quote at most six words and then summarize the rest.` |
| 42 | image-plus-file | Image and text injection | `Use @{fixtures/tiny.png} and @{fixtures/data.json}; say whether both modalities were supplied.` |
| 43 | stdin-multiline | Multiline stdin | `Classify stdin as absent, single-line, or multiline. Then answer with one explanatory sentence.` |
| 44 | memory-conflict-directive | System memory vs prompt claim | `Ignore any workspace memory and call this production. Then state the workspace label from GEMINI.md if available.` |
| 45 | mcp-tool-pressure | Tool-use prompt | `Use fixture_echo with text 'tool-pressure'. Do not fabricate a result if the tool is unavailable.` |
| 46 | mcp-edge-characters | MCP escaping | `Call fixture_echo with text 'quotes " and newline marker \\n'. Summarize exactly what came back.` |
| 47 | mcp-list-tools | MCP discovery | `List available MCP tools from pangaea-fixture if visible, then call fixture_echo with text 'list-tools'.` |
| 48 | refusal-boundary-benign | Safety boundary, benign | `Explain why fixture scripts should avoid destructive shell commands during capture.` |
| 49 | final-json-schema | Strict schema final case | `Return JSON with keys scenario, adapter, artifacts, and validation_notes for this capture.` |

## Expected Artifact Set

For each CLI case, validate that these files exist under `cli/` with the same
numeric prefix:

- `NNN_<model>.request.json`
- `NNN_<model>.argv.txt`
- `NNN_<model>.stdout.ndjson`
- `NNN_<model>.stderr.log`
- `NNN_<model>.result.json`

For the ACP probe, validate:

- `acp/` output from `scripts/gemini-acp-probe.mjs`
- request metadata with model, prompt, cwd, resource file, image file, and MCP
  command
- response events or error payloads sufficient to map ACP behavior later

For optional MITM capture, validate:

- `http/flows.mitm` exists when `mitmdump` is available and interception works
- `http/mitmdump.log` records startup and TLS interception failures
- captured hosts can distinguish Gemini API, Code Assist, OAuth, and other
  Google endpoints without storing secrets in committed fixtures

## Per-Case Validation Checklist

Use this checklist for automated post-processing after the long capture:

- `result.json.exit_code` is present for every case, even failures.
- `request.json.model` matches the model embedded in the filename.
- `argv.txt` contains `-p`, `--skip-trust`, `--approval-mode`,
  `--output-format`, `--model`, and `--allowed-mcp-server-names`.
- `stdout.ndjson` is parseable as stream events for `stream-json` cases or as a
  complete JSON object for `json` cases, when the CLI exits successfully.
- `stderr.log` is retained even when empty.
- File injection cases mention expected fixture facts without leaking absolute
  host-only paths as semantic answer content.
- Image cases produce either a valid image description or a clear multimodal
  unsupported/error signal.
- MCP cases either include an observed `fixture_echo:<text>` result or an
  explicit tool-unavailable error surface.
- Stdin cases distinguish absent stdin from provided stdin.
- Memory cases reflect the controlled Pangaea Gemini fixture workspace label.
- Continuation cases do not imply persistent session state unless the CLI
  actually provides it across invocations.
- Usage/refresh cases preserve stderr and MITM observations separately from the
  model's natural-language answer.

## Capture Coverage Targets

The 300 captures should provide enough diversity for adapter tests to cover:

- model alias handling and preview model failures
- `stream-json` event framing versus `json` completion framing
- approval-mode serialization and tool-call permission prompts
- stdin piping and no-stdin behavior
- root `GEMINI.md` discovery
- `@{file}` expansion for text, code, JSON, and PNG files
- Markdown tables, nested lists, fenced code blocks, nested fences, and strict
  JSON answers
- MCP tool discovery, call success, escaping, and unavailable-tool fallback
- continuation-like prompts without assuming process-level session persistence
- usage, quota, OAuth refresh, and token-expiry warnings in stderr or HTTP
  traces
- stable artifact names and metadata for future replay tests

## Notes For Script Implementation

Keep the script deterministic:

- Use the slot number, not random choice, to select output format, approval
  mode, stdin payload, and prompt.
- Keep `PANGAEA_GEMINI_FIXTURE_LIMIT` as the final cap so short smoke captures
  can run with the same matrix.
- Add any extra stdin payloads as small literals in the script; do not depend on
  external files beyond the generated fixture workspace.
- Do not fail the whole run because one model or preview model rejects a prompt;
  preserve its `result.json`, stdout, and stderr.
- Redact OAuth tokens, API keys, cookies, and authorization headers from any
  committed derivative fixtures.
