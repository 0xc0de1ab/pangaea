# ask

`ask` reads defaults from `~/.config/pangaea/ask-config.json` when the file exists.
Runtime precedence is:

1. CLI flags
2. Environment variables
3. Config file
4. Built-in defaults

Example:

```json
{
  "base_url": "https://pangaea.example.com/route/public/antigravity-sonnet",
  "api_key": "replace-with-router-key",
  "model": "claude-sonnet-4-6",
  "api": "responses",
  "stream": true,
  "spinner": true,
  "markdown_translator": "glow"
}
```

Use `--config /path/to/ask-config.json` or `PANGAEA_ASK_CONFIG` to select another config file.
Set `max_tokens` only when you explicitly want to cap the answer length.
Because this file can contain an API key, keep it user-readable only:

```bash
chmod 600 ~/.config/pangaea/ask-config.json
```

When `--tools` is enabled, `ask` exposes local file tools plus Codex-style
`search_files`, `exec_command`, and `apply_patch` tools under `--tool-root`.
