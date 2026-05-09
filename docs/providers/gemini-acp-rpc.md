# Gemini ACP JSON-RPC Notes

Gemini CLI `--acp` uses newline-delimited JSON-RPC 2.0 over stdio.

## Agent Methods

Observed from Gemini CLI 0.40.1 and 0.41.2:

- `initialize`
- `authenticate`
- `session/new`
- `session/load`
- `session/list`
- `session/fork`
- `session/resume`
- `session/close`
- `session/prompt`
- `session/set_mode`
- `session/set_model`
- `session/set_config_option`
- `session/cancel`

`initialize` requires `protocolVersion: 1`. The response reports image, audio,
embedded-context, and MCP http/sse capabilities.

Gemini CLI 0.41.2 exposes `session/close` in the ACP schema bundle, but a real
request returned JSON-RPC `Method not found`. The probe therefore does not call
`session/close` unless `PANGAEA_GEMINI_ACP_CLOSE=1` is set.

## Client Methods

Gemini may call back into the client with:

- `session/update`
- `session/request_permission`
- `fs/read_text_file`
- `fs/write_text_file`
- `terminal/create`
- `terminal/output`
- `terminal/wait_for_exit`
- `terminal/kill`
- `terminal/release`

The Pangaea ACP adapter should treat these as provider-originated events and
map them into canonical stream/tool-call events.

## Fixture Capture

Use:

```bash
PANGAEA_GEMINI_FIXTURE_LIMIT=300 \
PANGAEA_GEMINI_FIXTURE_MITM=auto \
scripts/capture-gemini-cli-fixtures.sh
```

The script writes CLI `stream-json`/`json`, ACP JSON-RPC, stderr, and optional
mitmproxy HTTP flow artifacts under `.tmp/gemini-fixtures`.

It also creates a controlled fixture workspace with:

- `GEMINI.md` system memory.
- `@{file}` prompt injections for Go, Markdown, JSON, and PNG fixtures.
- A local stdio MCP server named `pangaea-fixture`.
- ACP prompt blocks for embedded text resources and images.

Use `PANGAEA_GEMINI_FIXTURE_MODELS` to narrow model coverage during smoke
runs, for example `PANGAEA_GEMINI_FIXTURE_MODELS=gemini-2.5-flash`.
