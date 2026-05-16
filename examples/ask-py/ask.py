#!/usr/bin/env python3
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from io import StringIO
from pathlib import Path


DEFAULT_BASE_URL = "https://pangaea.example.com/route/public/antigravity-sonnet"
DEFAULT_MODEL = "claude-sonnet-4-6"
DEFAULT_API = "responses"
TOOL_CALL_START = "<tool_call>"
TOOL_CALL_END = "</tool_call>"

TOOL_SYSTEM_PROMPT = """You can use local tools when they are necessary to complete the user's request.
If the user asks you to create, edit, inspect, or list files, call the appropriate tool instead of only explaining the steps.
Only write files that are directly requested by the user. Keep paths relative unless the user explicitly asks otherwise.
When you call a tool, include a short Korean or English `intent` argument that explains why this tool call is needed."""

TOOL_DEFINITIONS_CHAT = [
    {
        "type": "function",
        "function": {
            "name": "write_file",
            "description": "Create or overwrite a UTF-8 text file under the configured tool root.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Relative path to write, for example a.yaml."},
                    "content": {"type": "string", "description": "Complete UTF-8 file content."},
                    "intent": {"type": "string", "description": "Short human-readable reason for this tool call."},
                },
                "required": ["path", "content"],
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read a UTF-8 text file under the configured tool root.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Relative path to read."},
                    "intent": {"type": "string", "description": "Short human-readable reason for this tool call."},
                },
                "required": ["path"],
                "additionalProperties": False,
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "list_files",
            "description": "List files under a directory in the configured tool root.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Relative directory path. Defaults to ."},
                    "intent": {"type": "string", "description": "Short human-readable reason for this tool call."},
                },
                "additionalProperties": False,
            },
        },
    },
]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Ask through a Pangaea OpenAI-compatible route.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument("prompt", nargs="*", help="Prompt text. Reads stdin when omitted.")
    parser.add_argument("--base-url", default=os.environ.get("PANGAEA_ASK_BASE_URL", DEFAULT_BASE_URL))
    parser.add_argument("--model", default=os.environ.get("PANGAEA_ASK_MODEL", DEFAULT_MODEL))
    parser.add_argument("--api", choices=("responses", "chat"), default=os.environ.get("PANGAEA_ASK_API", DEFAULT_API), help="OpenAI-compatible API shape to use.")
    parser.add_argument("--max-tokens", type=int, default=int(os.environ.get("PANGAEA_ASK_MAX_TOKENS", "1024")))
    parser.add_argument("--stream", action=argparse.BooleanOptionalAction, default=True, help="Use OpenAI SSE streaming responses.")
    parser.add_argument(
        "--tools",
        action=argparse.BooleanOptionalAction,
        default=os.environ.get("PANGAEA_ASK_TOOLS", "1").lower() not in ("0", "false", "no", "off"),
        help="Enable local file tools. Tool calls are executed under --tool-root.",
    )
    parser.add_argument("--tool-root", default=os.environ.get("PANGAEA_ASK_TOOL_ROOT", os.getcwd()), help="Directory that local file tools may access.")
    parser.add_argument("--tool-turns", type=int, default=int(os.environ.get("PANGAEA_ASK_TOOL_TURNS", "6")), help="Maximum tool-call round trips.")
    parser.add_argument(
        "--markdown-translator",
        choices=("plain", "rich", "glamour", "glow"),
        default=os.environ.get("PANGAEA_ASK_MARKDOWN_TRANSLATOR", "plain"),
        help="Translate Markdown output for the terminal. glow is an alias for glamour CLI rendering.",
    )
    parser.add_argument("--glamour-style", default=os.environ.get("PANGAEA_ASK_GLAMOUR_STYLE", "dark"), help="Glamour/glow style name.")
    parser.add_argument("--rich-code-theme", default=os.environ.get("PANGAEA_ASK_RICH_CODE_THEME", "monokai"), help="Rich/Pygments code highlighting theme.")
    return parser


def read_prompt(parts: list[str]) -> str:
    if parts:
        return " ".join(parts)
    if not sys.stdin.isatty():
        return sys.stdin.read().strip()
    raise SystemExit("prompt is required")


def api_key() -> str:
    key = os.environ.get("PANGAEA_ASK_API_KEY") or os.environ.get("OPENAI_API_KEY")
    if not key:
        raise SystemExit("PANGAEA_ASK_API_KEY or OPENAI_API_KEY is required")
    return key


def post_api(base_url: str, key: str, api: str, payload: dict) -> urllib.response.addinfourl:
    if api == "responses":
        path = "/v1/responses"
    else:
        path = "/v1/chat/completions"
    url = base_url.rstrip("/") + path
    req = urllib.request.Request(
        url,
        method="POST",
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers={
            "Authorization": "Bearer " + key,
            "Content-Type": "application/json",
            "Accept": "text/event-stream" if payload.get("stream") else "application/json",
        },
    )
    return urllib.request.urlopen(req, timeout=300)


def response_text(data: dict) -> str:
    if isinstance(data.get("output_text"), str):
        return data["output_text"]
    output = data.get("output")
    if isinstance(output, list):
        parts: list[str] = []
        for item in output:
            for content in item.get("content", []) if isinstance(item, dict) else []:
                text = content.get("text") if isinstance(content, dict) else None
                if isinstance(text, str):
                    parts.append(text)
        if parts:
            return "".join(parts)
    return data.get("choices", [{}])[0].get("message", {}).get("content", "")


def run_tool_loop(args: argparse.Namespace, prompt: str, key: str) -> int:
    root = Path(args.tool_root).expanduser().resolve()
    messages: list[dict] = [
        {"role": "system", "content": TOOL_SYSTEM_PROMPT + f"\nLocal tool root: {root}"},
        {"role": "user", "content": prompt},
    ]
    for _ in range(max(1, args.tool_turns)):
        payload = {
            "model": args.model,
            "messages": messages,
            "tools": TOOL_DEFINITIONS_CHAT,
            "tool_choice": "auto",
            "stream": args.stream,
            "max_tokens": args.max_tokens,
        }
        with post_api(args.base_url, key, "chat", payload) as resp:
            if args.stream:
                content, tool_calls = read_chat_stream_with_tools(resp, args)
            else:
                content, tool_calls = read_chat_buffered_with_tools(resp)
                if not tool_calls and content:
                    print_output(content, args)
        if not tool_calls:
            return 0
        messages.append({
            "role": "assistant",
            "content": content or "",
            "tool_calls": tool_calls,
        })
        for call in tool_calls:
            result = execute_tool_call(call, root)
            messages.append({
                "role": "tool",
                "tool_call_id": call.get("id") or "call_0",
                "content": result,
            })
    print(f"tool call limit reached after {args.tool_turns} turns", file=sys.stderr)
    return 1


def read_chat_buffered_with_tools(resp: urllib.response.addinfourl) -> tuple[str, list[dict]]:
    data = json.loads(resp.read().decode("utf-8"))
    return chat_response_content_and_tools(data)


def read_chat_stream_with_tools(resp: urllib.response.addinfourl, args: argparse.Namespace) -> tuple[str, list[dict]]:
    emitted = False
    content_parts: list[str] = []
    raw_lines: list[str] = []
    tool_call_pieces: dict[int, dict] = {}
    text_tool_filter = ToolCallTextFilter()
    stream_renderer = PlainStreamRenderer() if args.markdown_translator == "plain" else ANSIStreamingMarkdownRenderer(args)
    for raw in resp:
        line = raw.decode("utf-8", errors="replace").strip()
        if line:
            raw_lines.append(line)
        if not line or not line.startswith("data:"):
            continue
        data = line.removeprefix("data:").strip()
        if data == "[DONE]":
            break
        try:
            event = json.loads(data)
        except json.JSONDecodeError:
            continue
        text = stream_event_text(event)
        if text:
            visible_text = text_tool_filter.feed(text)
            if visible_text:
                emitted = True
                content_parts.append(visible_text)
                stream_renderer.feed(visible_text)
        merge_chat_stream_tool_calls(tool_call_pieces, event)
    visible_tail = text_tool_filter.finish()
    if visible_tail:
        emitted = True
        content_parts.append(visible_tail)
        stream_renderer.feed(visible_tail)
    tool_calls = normalized_stream_tool_calls(tool_call_pieces)
    tool_calls.extend(text_tool_filter.tool_calls)
    if emitted:
        stream_renderer.finish()
        return "".join(content_parts), tool_calls
    if raw_lines:
        try:
            data = json.loads("\n".join(raw_lines))
            content, buffered_tool_calls = chat_response_content_and_tools(data)
            if content and not buffered_tool_calls:
                print_output(content, args)
            return content, buffered_tool_calls
        except json.JSONDecodeError:
            pass
    return "", tool_calls


def chat_response_content_and_tools(data: dict) -> tuple[str, list[dict]]:
    choice = data.get("choices", [{}])[0]
    message = choice.get("message", {}) if isinstance(choice, dict) else {}
    content = message.get("content") or ""
    tool_calls = normalize_tool_calls(message.get("tool_calls") or [])
    visible_content, text_tool_calls = extract_text_tool_calls(content)
    if text_tool_calls:
        content = visible_content
        tool_calls.extend(text_tool_calls)
    return content, tool_calls


def merge_chat_stream_tool_calls(tool_call_pieces: dict[int, dict], event: dict) -> None:
    choices = event.get("choices")
    if not isinstance(choices, list) or not choices:
        return
    delta = choices[0].get("delta", {}) if isinstance(choices[0], dict) else {}
    pieces = delta.get("tool_calls") if isinstance(delta, dict) else None
    if not isinstance(pieces, list):
        return
    for piece in pieces:
        if not isinstance(piece, dict):
            continue
        index = piece.get("index")
        if not isinstance(index, int):
            index = len(tool_call_pieces)
        slot = tool_call_pieces.setdefault(index, {"id": "", "type": "function", "function": {"name": "", "arguments": ""}})
        if piece.get("id"):
            slot["id"] = piece["id"]
        if piece.get("type"):
            slot["type"] = piece["type"]
        function = piece.get("function")
        if not isinstance(function, dict):
            continue
        slot_function = slot.setdefault("function", {"name": "", "arguments": ""})
        if function.get("name"):
            if not slot_function.get("name"):
                slot_function["name"] = function["name"]
            elif function["name"] != slot_function["name"]:
                slot_function["name"] += function["name"]
        if function.get("arguments"):
            slot_function["arguments"] = (slot_function.get("arguments") or "") + function["arguments"]


def normalized_stream_tool_calls(tool_call_pieces: dict[int, dict]) -> list[dict]:
    calls = [tool_call_pieces[index] for index in sorted(tool_call_pieces)]
    return normalize_tool_calls(calls)


class ToolCallTextFilter:
    def __init__(self) -> None:
        self.buffer = ""
        self.in_tool_call = False
        self.tool_call_buffer = ""
        self.tool_calls: list[dict] = []

    def feed(self, text: str) -> str:
        self.buffer += text
        visible: list[str] = []
        while self.buffer:
            if self.in_tool_call:
                end = self.buffer.find(TOOL_CALL_END)
                if end == -1:
                    hold = partial_tag_suffix_len(self.buffer, TOOL_CALL_END)
                    if hold:
                        self.tool_call_buffer += self.buffer[:-hold]
                        self.buffer = self.buffer[-hold:]
                    else:
                        self.tool_call_buffer += self.buffer
                        self.buffer = ""
                    break
                self.tool_call_buffer += self.buffer[:end]
                self._append_tool_call(self.tool_call_buffer)
                self.tool_call_buffer = ""
                self.in_tool_call = False
                self.buffer = self.buffer[end + len(TOOL_CALL_END):]
                continue

            start = self.buffer.find(TOOL_CALL_START)
            if start == -1:
                hold = partial_tag_suffix_len(self.buffer, TOOL_CALL_START)
                if hold:
                    visible.append(self.buffer[:-hold])
                    self.buffer = self.buffer[-hold:]
                else:
                    visible.append(self.buffer)
                    self.buffer = ""
                break
            visible.append(self.buffer[:start])
            self.buffer = self.buffer[start + len(TOOL_CALL_START):]
            self.in_tool_call = True
        return "".join(visible)

    def finish(self) -> str:
        if self.in_tool_call:
            payload = self.tool_call_buffer + self.buffer
            if self._append_tool_call(payload):
                self.buffer = ""
                self.tool_call_buffer = ""
                self.in_tool_call = False
                return ""
            visible = TOOL_CALL_START + payload
            self.buffer = ""
            self.tool_call_buffer = ""
            self.in_tool_call = False
            return visible
        visible = self.buffer
        self.buffer = ""
        return visible

    def _append_tool_call(self, payload: str) -> bool:
        call = text_tool_call_to_openai(payload, len(self.tool_calls))
        if call is None:
            return False
        self.tool_calls.append(call)
        return True


def partial_tag_suffix_len(text: str, tag: str) -> int:
    max_len = min(len(text), len(tag) - 1)
    for size in range(max_len, 0, -1):
        if tag.startswith(text[-size:]):
            return size
    return 0


def extract_text_tool_calls(content: str) -> tuple[str, list[dict]]:
    if not content:
        return "", []
    text_filter = ToolCallTextFilter()
    visible = text_filter.feed(content) + text_filter.finish()
    return visible, text_filter.tool_calls


def text_tool_call_to_openai(payload: str, index: int) -> dict | None:
    try:
        data = parse_json_object_lenient(payload)
    except ValueError as exc:
        print(f"warning: could not parse text tool call: {exc}", file=sys.stderr)
        return None
    function = data.get("function") if isinstance(data.get("function"), dict) else None
    if function is not None:
        name = function.get("name")
        arguments = function.get("arguments", {})
    else:
        name = data.get("name")
        arguments = data.get("arguments", {})
    if not isinstance(name, str) or not name.strip():
        print("warning: text tool call has no function name", file=sys.stderr)
        return None
    if isinstance(arguments, str):
        raw_arguments = arguments
    else:
        raw_arguments = json.dumps(arguments or {}, ensure_ascii=False)
    return {
        "id": f"text_call_{index}",
        "type": "function",
        "function": {
            "name": name.strip(),
            "arguments": raw_arguments,
        },
    }


def parse_json_object_lenient(raw: str) -> dict:
    payload = raw.strip()
    if not payload:
        raise ValueError("empty payload")
    for candidate in (payload, escape_json_string_control_chars(payload)):
        try:
            data = json.loads(candidate)
        except json.JSONDecodeError:
            continue
        if isinstance(data, dict):
            return data
        raise ValueError("payload is not a JSON object")
    start = payload.find("{")
    end = payload.rfind("}")
    if start >= 0 and end > start:
        candidate = escape_json_string_control_chars(payload[start:end + 1])
        try:
            data = json.loads(candidate)
        except json.JSONDecodeError as exc:
            raise ValueError(str(exc)) from exc
        if isinstance(data, dict):
            return data
    raise ValueError("invalid JSON object")


def escape_json_string_control_chars(raw: str) -> str:
    out: list[str] = []
    in_string = False
    escaped = False
    for ch in raw:
        if escaped:
            out.append(ch)
            escaped = False
            continue
        if ch == "\\":
            out.append(ch)
            escaped = True
            continue
        if ch == '"':
            out.append(ch)
            in_string = not in_string
            continue
        if in_string and ch == "\n":
            out.append("\\n")
            continue
        if in_string and ch == "\r":
            out.append("\\r")
            continue
        if in_string and ch == "\t":
            out.append("\\t")
            continue
        out.append(ch)
    return "".join(out)


def normalize_tool_calls(calls: list[dict]) -> list[dict]:
    normalized: list[dict] = []
    for index, call in enumerate(calls):
        if not isinstance(call, dict):
            continue
        function = call.get("function") if isinstance(call.get("function"), dict) else {}
        name = function.get("name")
        arguments = function.get("arguments")
        if not isinstance(name, str) or not name:
            continue
        if not isinstance(arguments, str):
            arguments = json.dumps(arguments or {}, ensure_ascii=False)
        normalized.append({
            "id": call.get("id") or f"call_{index}",
            "type": call.get("type") or "function",
            "function": {
                "name": name,
                "arguments": arguments,
            },
        })
    return normalized


def execute_tool_call(call: dict, root: Path) -> str:
    function = call.get("function") if isinstance(call.get("function"), dict) else {}
    name = function.get("name")
    arguments: dict = {}
    try:
        arguments = decode_tool_arguments(function.get("arguments") or "{}")
        if name == "write_file":
            result = tool_write_file(root, arguments)
        elif name == "read_file":
            result = tool_read_file(root, arguments)
        elif name == "list_files":
            result = tool_list_files(root, arguments)
        else:
            result = {"ok": False, "error": f"unknown tool: {name}"}
    except Exception as exc:
        result = {"ok": False, "error": str(exc)}
    print_tool_call_status(name, arguments, result)
    return json.dumps(result, ensure_ascii=False)


def decode_tool_arguments(raw: str) -> dict:
    data = json.loads(raw)
    if not isinstance(data, dict):
        raise ValueError("tool arguments must be a JSON object")
    return data


def tool_write_file(root: Path, arguments: dict) -> dict:
    path = arguments.get("path")
    content = arguments.get("content")
    if not isinstance(path, str) or not path.strip():
        raise ValueError("write_file.path is required")
    if not isinstance(content, str):
        raise ValueError("write_file.content must be a string")
    target = resolve_tool_path(root, path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content, encoding="utf-8")
    return {
        "ok": True,
        "path": relative_tool_path(root, target),
        "bytes": len(content.encode("utf-8")),
    }


def tool_read_file(root: Path, arguments: dict) -> dict:
    path = arguments.get("path")
    if not isinstance(path, str) or not path.strip():
        raise ValueError("read_file.path is required")
    target = resolve_tool_path(root, path)
    data = target.read_bytes()
    limit = 128 * 1024
    truncated = len(data) > limit
    text = data[:limit].decode("utf-8", errors="replace")
    return {
        "ok": True,
        "path": relative_tool_path(root, target),
        "content": text,
        "truncated": truncated,
        "bytes": len(data),
    }


def tool_list_files(root: Path, arguments: dict) -> dict:
    path = arguments.get("path") or "."
    if not isinstance(path, str):
        raise ValueError("list_files.path must be a string")
    target = resolve_tool_path(root, path)
    if not target.is_dir():
        raise ValueError(f"not a directory: {relative_tool_path(root, target)}")
    entries = []
    for child in sorted(target.iterdir(), key=lambda item: item.name.lower())[:200]:
        stat = child.stat()
        entries.append({
            "name": child.name,
            "path": relative_tool_path(root, child),
            "type": "directory" if child.is_dir() else "file",
            "bytes": stat.st_size,
        })
    return {
        "ok": True,
        "path": relative_tool_path(root, target),
        "entries": entries,
        "truncated": len(entries) >= 200,
    }


def resolve_tool_path(root: Path, requested: str) -> Path:
    requested_path = Path(requested).expanduser()
    target = requested_path.resolve() if requested_path.is_absolute() else (root / requested_path).resolve()
    try:
        target.relative_to(root)
    except ValueError as exc:
        raise ValueError(f"path escapes tool root: {requested}") from exc
    return target


def relative_tool_path(root: Path, target: Path) -> str:
    try:
        return str(target.relative_to(root))
    except ValueError:
        return str(target)


def print_tool_call_status(name: str | None, arguments: dict, result: dict) -> None:
    tool_name = name or "unknown"
    path = arguments.get("path")
    status = "ok" if result.get("ok") else "error"
    status_color = "\033[32m" if result.get("ok") else "\033[31m"
    detail = ""
    if isinstance(path, str) and path:
        detail = f" \033[2m{path}\033[0m"
    if result.get("ok") and isinstance(result.get("bytes"), int):
        detail += f" \033[2m{format_bytes(result['bytes'])}\033[0m"
    elif not result.get("ok") and result.get("error"):
        detail += f" \033[2m{result['error']}\033[0m"
    print(f"\033[36m*\033[0m \033[1mtool\033[0m \033[33m{tool_name}\033[0m{detail} {status_color}{status}\033[0m", file=sys.stderr)
    print(f"  \033[36mintent\033[0m {tool_call_intent(tool_name, arguments)}", file=sys.stderr)
    print(f"  \033[36margs\033[0m {format_tool_args(arguments)}", file=sys.stderr)


def tool_call_intent(name: str, arguments: dict) -> str:
    intent = arguments.get("intent")
    if isinstance(intent, str) and intent.strip():
        return intent.strip()
    path = arguments.get("path")
    if isinstance(path, str) and path:
        if name == "write_file":
            return f"write requested file {path}"
        if name == "read_file":
            return f"inspect file {path}"
        if name == "list_files":
            return f"list files under {path}"
    return f"run {name}"


def format_tool_args(arguments: dict) -> str:
    display = {}
    for key, value in arguments.items():
        if key == "intent":
            continue
        if key == "content" and isinstance(value, str):
            display["content_bytes"] = len(value.encode("utf-8"))
            preview = compact_preview(value)
            if preview:
                display["content_preview"] = preview
            continue
        display[key] = value
    return json.dumps(display, ensure_ascii=False, sort_keys=True)


def compact_preview(value: str, limit: int = 96) -> str:
    preview = " ".join(value.strip().split())
    if len(preview) > limit:
        return preview[:limit - 1] + "…"
    return preview


def format_bytes(value: int) -> str:
    if value < 1024:
        return f"{value}B"
    if value < 1024 * 1024:
        return f"{value / 1024:.1f}KB"
    return f"{value / (1024 * 1024):.1f}MB"


def print_buffered(resp: urllib.response.addinfourl, args: argparse.Namespace) -> None:
    data = json.loads(resp.read().decode("utf-8"))
    print_output(response_text(data), args)


def print_stream(resp: urllib.response.addinfourl, args: argparse.Namespace) -> None:
    emitted = False
    raw_lines: list[str] = []
    if args.markdown_translator == "plain":
        stream_renderer = PlainStreamRenderer()
    else:
        stream_renderer = ANSIStreamingMarkdownRenderer(args)
    for raw in resp:
        line = raw.decode("utf-8", errors="replace").strip()
        if line:
            raw_lines.append(line)
        if not line or not line.startswith("data:"):
            continue
        data = line.removeprefix("data:").strip()
        if data == "[DONE]":
            break
        try:
            event = json.loads(data)
        except json.JSONDecodeError:
            continue
        text = stream_event_text(event)
        if text:
            emitted = True
            stream_renderer.feed(text)
    if not emitted and raw_lines:
        try:
            data = json.loads("\n".join(raw_lines))
            content = response_text(data)
            if content:
                stream_renderer.feed(content)
        except json.JSONDecodeError:
            pass
    stream_renderer.finish()


def stream_event_text(event: dict) -> str:
    if event.get("type") == "response.output_text.delta":
        return event.get("delta") or ""
    delta = event.get("choices", [{}])[0].get("delta", {})
    if isinstance(delta, dict):
        return delta.get("content") or ""
    return ""


def print_output(markdown: str, args: argparse.Namespace) -> None:
    if args.markdown_translator in ("glamour", "glow"):
        rendered = render_with_glamour(markdown, args.glamour_style)
        if rendered is not None:
            print(rendered, end="" if rendered.endswith("\n") else "\n")
            return
    if args.markdown_translator == "rich":
        rendered = render_with_rich(markdown, args.rich_code_theme)
        if rendered is not None:
            print(rendered, end="" if rendered.endswith("\n") else "\n")
            return
    print(markdown)


class PlainStreamRenderer:
    def feed(self, text: str) -> None:
        print(text, end="", flush=True)

    def finish(self) -> None:
        print()


class ANSIStreamingMarkdownRenderer:
    _fence_re = re.compile(r"^\s*```([A-Za-z0-9_+.#-]*)\s*$")
    _heading_re = re.compile(r"^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$")

    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.pending = ""
        self.in_code = False
        self.code_language = ""
        self.table_candidate = ""
        self.table_lines: list[str] = []
        self.list_lines: list[str] = []

    def feed(self, text: str) -> None:
        self.pending += text
        while "\n" in self.pending:
            line, self.pending = self.pending.split("\n", 1)
            self._write_line(line + "\n")
        if self.pending and not self.in_code and not self._should_hold_pending(self.pending):
            self._write(self.pending)
            self.pending = ""

    def finish(self) -> None:
        if self.pending:
            self._write_line(self.pending)
            self.pending = ""
        self._flush_table_candidate()
        self._flush_table()
        self._flush_list()
        print()

    def _write_line(self, line: str) -> None:
        stripped = line.rstrip("\n")
        match = self._fence_re.match(stripped)
        if match:
            self._flush_table_candidate()
            self._flush_table()
            self._flush_list()
            self.in_code = not self.in_code
            self.code_language = match.group(1).strip() if self.in_code else ""
            self._write(self._dim(line))
            return
        if self.in_code:
            self._write(highlight_code_line(line, self.code_language, self.args))
            return
        heading = self._heading_re.match(stripped)
        if heading:
            self._flush_table_candidate()
            self._flush_table()
            self._flush_list()
            self._write(format_heading(len(heading.group(1)), heading.group(2)) + "\n")
            return
        if self.list_lines:
            if is_markdown_list_continuation(stripped):
                self.list_lines.append(line)
                return
            self._flush_list()
            self._write_line(line)
            return
        if self.table_lines:
            if is_markdown_table_row(stripped):
                self.table_lines.append(line)
                return
            self._flush_table()
            self._write_line(line)
            return
        if self.table_candidate:
            if is_markdown_table_separator(stripped):
                self.table_lines = [self.table_candidate, line]
                self.table_candidate = ""
                return
            self._flush_table_candidate()
        if is_markdown_table_row(stripped):
            self._flush_list()
            self.table_candidate = line
            return
        if is_markdown_list_item(stripped):
            self._flush_table_candidate()
            self._flush_table()
            self.list_lines = [line]
            return
        self._write(line)

    def _write(self, text: str) -> None:
        sys.stdout.write(text)
        sys.stdout.flush()

    def _maybe_partial_fence(self, text: str) -> bool:
        return text.lstrip().startswith("```")

    def _should_hold_pending(self, text: str) -> bool:
        stripped = text.lstrip()
        return (
            self._maybe_partial_fence(text)
            or bool(self.table_candidate)
            or bool(self.table_lines)
            or bool(self.list_lines)
            or stripped.startswith("|")
            or stripped.startswith("#")
            or bool(re.match(r"^(\s*)([-+*]|\d+[.)])(\s+|$)", text))
            or re.fullmatch(r"\s*\d+[.)]?", text) is not None
        )

    def _dim(self, text: str) -> str:
        if not text:
            return text
        return f"\033[2m{text}\033[0m"

    def _flush_table_candidate(self) -> None:
        if self.table_candidate:
            self._write(self.table_candidate)
            self.table_candidate = ""

    def _flush_table(self) -> None:
        if not self.table_lines:
            return
        markdown = "".join(self.table_lines)
        self.table_lines = []
        rendered = render_with_glamour(markdown, self.args.glamour_style)
        if rendered is None:
            self._write(markdown)
            return
        self._write(rendered)
        if not rendered.endswith("\n"):
            self._write("\n")

    def _flush_list(self) -> None:
        if not self.list_lines:
            return
        markdown = "".join(self.list_lines)
        self.list_lines = []
        rendered = render_with_glamour(markdown, self.args.glamour_style)
        if rendered is None:
            self._write(markdown)
            return
        self._write(rendered)
        if not rendered.endswith("\n"):
            self._write("\n")


def is_markdown_table_row(line: str) -> bool:
    stripped = line.strip()
    return stripped.startswith("|") and stripped.endswith("|") and stripped.count("|") >= 2


def is_markdown_table_separator(line: str) -> bool:
    if not is_markdown_table_row(line):
        return False
    cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
    if not cells:
        return False
    return all(re.fullmatch(r":?-{3,}:?", cell or "") is not None for cell in cells)


def is_markdown_list_item(line: str) -> bool:
    return re.match(r"^\s{0,12}(?:[-+*]|\d+[.)])\s+\S", line) is not None


def is_markdown_list_continuation(line: str) -> bool:
    if line.strip() == "":
        return True
    if is_markdown_list_item(line):
        return True
    return re.match(r"^\s{2,}\S", line) is not None


def format_heading(level: int, text: str) -> str:
    text = text.strip()
    if level <= 1:
        return f"\033[1;35m{text}\033[0m"
    if level == 2:
        return f"\033[1;36m{text}\033[0m"
    if level == 3:
        return f"\033[1;34m{text}\033[0m"
    return f"\033[1m{text}\033[0m"


def highlight_code_line(line: str, language: str, args: argparse.Namespace) -> str:
    try:
        from pygments import highlight
        from pygments.formatters import Terminal256Formatter
        from pygments.lexers import TextLexer, get_lexer_by_name
    except ImportError:
        return line
    try:
        lexer = get_lexer_by_name(language or "text")
    except Exception:
        lexer = TextLexer()
    style = args.glamour_style if args.markdown_translator in ("glamour", "glow") else args.rich_code_theme
    if style in ("auto", "dark", "light"):
        style = "monokai" if style != "light" else "default"
    try:
        return highlight(line, lexer, Terminal256Formatter(style=style))
    except Exception:
        return line


def render_with_glamour(markdown: str, style: str) -> str | None:
    if not markdown:
        return ""
    width = max(40, shutil.get_terminal_size((100, 24)).columns)
    if glamour := find_executable("glamour"):
        commands = [[glamour, "-s", style, "-w", str(width)]]
    elif glow := find_executable("glow"):
        commands = [[glow, "-s", style, "-w", str(width), "-"]]
    else:
        print("warning: --markdown-translator glamour requested, but neither glamour nor glow is installed", file=sys.stderr)
        return None
    for command in commands:
        try:
            env = os.environ.copy()
            env.pop("NO_COLOR", None)
            env.update({"CLICOLOR_FORCE": "1", "FORCE_COLOR": "1", "TERM": env.get("TERM") or "xterm-256color"})
            result = subprocess.run(command, input=markdown, text=True, capture_output=True, check=True, env=env)
            return result.stdout
        except (OSError, subprocess.CalledProcessError) as exc:
            print(f"warning: glamour renderer failed: {exc}", file=sys.stderr)
    return None


def render_with_rich(markdown: str, code_theme: str) -> str | None:
    if not markdown:
        return ""
    try:
        from rich.console import Console
        from rich.markdown import Markdown
    except ImportError:
        print("warning: --markdown-translator rich requested, but Python rich is not installed", file=sys.stderr)
        return None
    buffer = StringIO()
    console = Console(file=buffer, force_terminal=True, color_system="truecolor", no_color=False, width=shutil.get_terminal_size((100, 24)).columns)
    console.print(Markdown(markdown, code_theme=code_theme))
    return buffer.getvalue()


def find_executable(name: str) -> str | None:
    if path := shutil.which(name):
        return path
    script_dir = Path(__file__).resolve().parent
    local = script_dir / "bin" / name
    if local.exists() and os.access(local, os.X_OK):
        return str(local)
    return None


def main() -> int:
    args = build_parser().parse_args()
    prompt = read_prompt(args.prompt)
    key = api_key()
    if args.tools:
        try:
            return run_tool_loop(args, prompt, key)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            print(f"HTTP {exc.code}: {body}", file=sys.stderr)
            return 1
        except urllib.error.URLError as exc:
            print(f"request failed: {exc}", file=sys.stderr)
            return 1
    if args.api == "responses":
        payload = {
            "model": args.model,
            "input": prompt,
            "stream": args.stream,
            "max_output_tokens": args.max_tokens,
        }
    else:
        payload = {
            "model": args.model,
            "messages": [{"role": "user", "content": prompt}],
            "stream": args.stream,
            "max_tokens": args.max_tokens,
        }
    try:
        with post_api(args.base_url, key, args.api, payload) as resp:
            if args.stream:
                print_stream(resp, args)
            else:
                print_buffered(resp, args)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        print(f"HTTP {exc.code}: {body}", file=sys.stderr)
        return 1
    except urllib.error.URLError as exc:
        print(f"request failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
