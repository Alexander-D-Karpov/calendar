from __future__ import annotations

import json
import re
from datetime import datetime, tzinfo
from pathlib import Path
from typing import Any


# Codex launches the configured MCP server during thread_start but returns
# without waiting for the child process, so the bridge unlinks its one-shot
# capability a short moment after the call returns (~0.4s measured against
# openai-codex 0.154.0). Long enough to absorb a slow host, short enough that a
# runtime deferring MCP startup to first tool use still fails closed. Lives here
# so the request path and the runtime smoke cannot drift apart.
MCP_CAPABILITY_TIMEOUT = 10.0


def local_time_label(value: Any, tz: tzinfo) -> str:
    """Render an API timestamp as HH:MM in the reader's own timezone.

    The API mixes offsets within one response ("...Z" next to "...+03:00"), so
    the raw values cannot be read or compared at a glance. Anything that does
    not parse is handed back untouched rather than dropped, and a bare date
    becomes "all day" instead of a misleading 00:00.
    """
    text = str(value or "").strip()
    if not text:
        return ""
    if re.fullmatch(r"\d{4}-\d{2}-\d{2}", text):
        return "all day"
    try:
        moment = datetime.fromisoformat(text[:-1] + "+00:00" if text.endswith("Z") else text)
    except ValueError:
        return text
    if moment.tzinfo is not None:
        moment = moment.astimezone(tz)
    return moment.strftime("%H:%M")


def normalize_tool_name(name: str) -> str:
    return re.sub(r"[^a-z0-9]", "", name.lower())


def safe_filename(name: str | None, fallback: str = "file") -> str:
    base = Path(name or fallback).name
    base = re.sub(r"[^A-Za-z0-9._ -]", "_", base).strip(" .")
    return base[:180] or fallback


def result_to_jsonish(result: Any) -> Any:
    if result is None:
        return None
    structured = getattr(result, "structured_content", None)
    if structured is not None:
        return structured
    content = getattr(result, "content", None)
    if content:
        texts = []
        for block in content:
            text = getattr(block, "text", None)
            if text is not None:
                texts.append(text)
        joined = "\n".join(texts).strip()
        if joined:
            try:
                return json.loads(joined)
            except json.JSONDecodeError:
                return joined
    if hasattr(result, "model_dump"):
        return result.model_dump(by_alias=True, exclude_none=True)
    return result


def parse_agent_response(text: Any) -> dict[str, Any]:
    if isinstance(text, dict):
        obj = text
        status = obj.get("status") if obj.get("status") in {"ask", "done"} else "done"
        return {
            "status": status,
            "text": str(obj.get("text") or "Done."),
            "refs": [str(x) for x in (obj.get("refs") or [])][:20],
        }
    raw = str(text or "").strip()
    if not raw:
        return {"status": "done", "text": "Done.", "refs": []}
    try:
        obj = json.loads(raw)
    except json.JSONDecodeError:
        match = re.search(r"\{.*\}", raw, re.S)
        if not match:
            return {"status": "done", "text": raw, "refs": []}
        try:
            obj = json.loads(match.group(0))
        except json.JSONDecodeError:
            return {"status": "done", "text": raw, "refs": []}
    if not isinstance(obj, dict):
        return {"status": "done", "text": raw, "refs": []}
    status = obj.get("status") if obj.get("status") in {"ask", "done"} else "done"
    return {
        "status": status,
        "text": str(obj.get("text") or "Done."),
        "refs": [str(x) for x in (obj.get("refs") or [])][:20],
    }
