#!/usr/bin/env python3
"""Best-effort Telegram channel purge using the Bot API.

Required env:
  TELEGRAM_API_TOKEN   Bot token from BotFather.
  TELEGRAM_CHAT_ID     Target channel/chat id, for example -1001234567890.

Example:
  TELEGRAM_API_TOKEN=... TELEGRAM_CHAT_ID=... \
    scripts/telegram-channel-purge.py --scan-limit 20000

Bot API cannot enumerate arbitrary channel history. This script obtains a
latest message_id by sending a temporary marker unless --latest-id is supplied,
then scans message IDs downward. It uses deleteMessages for fast batches and
recursively splits failed batches down to single message IDs, ignoring IDs that
Telegram refuses to delete.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any


DEFAULT_ENDPOINT = "https://api.telegram.org"


@dataclass
class Stats:
    ranges_attempted: int = 0
    ids_attempted: int = 0
    batch_ok: int = 0
    batch_failed: int = 0
    single_ok: int = 0
    single_failed: int = 0
    max_depth: int = 0
    failures: dict[str, int] = field(default_factory=dict)

    def add_failure(self, description: str) -> None:
        key = description.strip() or "unknown failure"
        self.failures[key] = self.failures.get(key, 0) + 1


class TelegramClient:
    def __init__(self, token: str, endpoint: str = DEFAULT_ENDPOINT, timeout: float = 30.0) -> None:
        self.base_url = endpoint.rstrip("/") + "/bot" + token
        self.timeout = timeout

    def post(self, method: str, payload: dict[str, Any]) -> tuple[int, dict[str, Any]]:
        body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        req = urllib.request.Request(
            self.base_url + "/" + method,
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        while True:
            try:
                with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                    return resp.status, json.loads(resp.read().decode("utf-8"))
            except urllib.error.HTTPError as exc:
                raw = exc.read().decode("utf-8", "replace")
                try:
                    parsed = json.loads(raw)
                except json.JSONDecodeError:
                    parsed = {"ok": False, "description": raw[:300]}
                retry_after = (parsed.get("parameters") or {}).get("retry_after")
                if exc.code == 429 and isinstance(retry_after, int) and retry_after > 0:
                    time.sleep(retry_after + 1)
                    continue
                return exc.code, parsed


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Best-effort purge Telegram channel messages.")
    parser.add_argument("--endpoint", default=os.environ.get("TELEGRAM_API_ENDPOINT", DEFAULT_ENDPOINT))
    parser.add_argument("--token-env", default="TELEGRAM_API_TOKEN")
    parser.add_argument("--chat-id-env", default="TELEGRAM_CHAT_ID")
    parser.add_argument("--latest-id", type=int, default=0, help="Newest message_id to start from.")
    parser.add_argument("--oldest-id", type=int, default=1, help="Oldest message_id to include.")
    parser.add_argument("--scan-limit", type=int, default=5000, help="How many IDs to scan downward.")
    parser.add_argument("--batch-size", type=int, default=100, help="deleteMessages batch size, max 100.")
    parser.add_argument("--sleep", type=float, default=0.05, help="Sleep between API calls.")
    parser.add_argument("--marker-text", default="pangaea telegram cleanup marker")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


def require_env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        print(f"missing required env: {name}", file=sys.stderr)
        raise SystemExit(2)
    return value


def chunk_desc(ids: list[int]) -> str:
    if not ids:
        return "empty"
    return f"{min(ids)}..{max(ids)}"


def delete_range(client: TelegramClient, chat_id: str, ids: list[int], args: argparse.Namespace, stats: Stats, depth: int = 0) -> None:
    if not ids:
        return
    stats.ranges_attempted += 1
    stats.ids_attempted += len(ids)
    stats.max_depth = max(stats.max_depth, depth)
    if args.dry_run:
        stats.batch_ok += 1
        return

    if len(ids) == 1:
        _, resp = client.post("deleteMessage", {"chat_id": chat_id, "message_id": ids[0]})
        if resp.get("ok"):
            stats.single_ok += 1
        else:
            stats.single_failed += 1
            stats.add_failure(resp.get("description") or "deleteMessage failed")
        time.sleep(args.sleep)
        return

    _, resp = client.post("deleteMessages", {"chat_id": chat_id, "message_ids": ids})
    if resp.get("ok"):
        stats.batch_ok += 1
        time.sleep(args.sleep)
        return

    stats.batch_failed += 1
    if len(ids) <= 2:
        for mid in ids:
            delete_range(client, chat_id, [mid], args, stats, depth + 1)
        return

    half = len(ids) // 2
    delete_range(client, chat_id, ids[:half], args, stats, depth + 1)
    delete_range(client, chat_id, ids[half:], args, stats, depth + 1)


def main() -> int:
    args = parse_args()
    token = require_env(args.token_env)
    chat_id = require_env(args.chat_id_env)
    if args.batch_size < 1 or args.batch_size > 100:
        print("--batch-size must be between 1 and 100", file=sys.stderr)
        return 2
    if args.scan_limit < 1:
        print("--scan-limit must be positive", file=sys.stderr)
        return 2

    client = TelegramClient(token, args.endpoint)
    latest_id = args.latest_id
    if latest_id <= 0:
        if args.dry_run:
            print("--dry-run requires --latest-id", file=sys.stderr)
            return 2
        _, resp = client.post(
            "sendMessage",
            {"chat_id": chat_id, "text": args.marker_text, "disable_notification": True},
        )
        if not resp.get("ok"):
            print(json.dumps({"stage": "send_marker", "response": resp}, ensure_ascii=False), file=sys.stderr)
            return 1
        latest_id = int(resp["result"]["message_id"])

    oldest_id = max(args.oldest_id, latest_id - args.scan_limit + 1)
    ids = list(range(latest_id, oldest_id - 1, -1))
    stats = Stats()

    print(
        json.dumps(
            {
                "stage": "start",
                "chat_id": str(chat_id)[:4] + "..." + str(chat_id)[-3:],
                "latest_id": latest_id,
                "oldest_id": oldest_id,
                "scan_count": len(ids),
                "batch_size": args.batch_size,
                "dry_run": args.dry_run,
            },
            sort_keys=True,
        )
    )

    for start in range(0, len(ids), args.batch_size):
        delete_range(client, chat_id, ids[start : start + args.batch_size], args, stats)

    print(json.dumps({"stage": "done", "stats": stats.__dict__}, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
