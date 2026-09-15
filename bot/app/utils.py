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


_TOOL_LABELS = {
    "listevents": "Reading your calendar",
    "getevent": "Reading your calendar",
    "search": "Searching",
    "listtodos": "Reading your todos",
    "gettodo": "Reading your todos",
    "listcalendars": "Reading your calendars",
    "listchanges": "Checking recent changes",
    "getaccount": "Checking your settings",
    "createevent": "Creating an event",
    "updateevent": "Updating an event",
    "deleteevent": "Deleting an event",
    "createtodo": "Creating a todo",
    "updatetodo": "Updating a todo",
    "deletetodo": "Deleting a todo",
    "searchbotcontext": "Looking through earlier requests",
}


def progress_event(event: Any) -> tuple[str, str] | None:
    """Classify a turn notification as ("tool" | "thinking" | "note", text).

    Reasoning arrives as deltas that are worth showing while the model works.
    The agent message also arrives as deltas, but the reply is a structured
    object, so those would render half-built JSON: they are matched by method
    and deliberately ignored.
    """
    payload = getattr(event, "payload", None)
    method = str(getattr(event, "method", "") or "").lower()
    if payload is None:
        return None

    delta = getattr(payload, "delta", None)
    if isinstance(delta, str) and delta:
        return ("thinking", delta) if "reasoning" in method else None

    message = getattr(payload, "message", None)
    if isinstance(message, str) and message and "mcp" in method:
        return ("note", message)

    label = progress_label(event)
    return ("tool", label) if label else None


def progress_label(event: Any) -> str | None:
    """Turn a Codex turn notification into one line a person can read.

    Deliberately duck-typed and defensive: the payload shapes come from a
    pinned but fast-moving SDK, and a progress line is never worth failing a
    request over, so anything unrecognised simply produces no update.
    """
    payload = getattr(event, "payload", None)
    method = str(getattr(event, "method", "") or "")
    if payload is None:
        return None

    name = _tool_name_of(payload)
    if name:
        # Tool names reach us namespaced by the MCP server, so match on the
        # suffix the way the guard resolves them rather than on equality.
        normalized = normalize_tool_name(name)
        if normalized in _TOOL_LABELS:
            return _TOOL_LABELS[normalized]
        for known, label in _TOOL_LABELS.items():
            if normalized.endswith(known):
                return label
        return f"Calling {name}"
    if method.startswith("item/started"):
        kind = str(getattr(getattr(payload, "item", None), "item_type", "") or "")
        if "command" in kind.lower():
            return "Running a command"
        if "reasoning" in kind.lower():
            return "Thinking"
    if method.startswith("turn/started"):
        return "Thinking"
    return None


def _tool_name_of(payload: Any) -> str:
    for holder in (payload, getattr(payload, "item", None)):
        if holder is None:
            continue
        for attr in ("tool_name", "tool", "name"):
            value = getattr(holder, attr, None)
            if isinstance(value, str) and value:
                return value
    return ""


_FENCE = re.compile(r"```(\w*)\n?(.*?)```", re.S)
_INLINE_CODE = re.compile(r"`([^`\n]+)`")
_LINK = re.compile(r"\[([^\]\n]+)\]\(([^)\s]+)\)")
_BOLD = re.compile(r"(?<!\w)(?:\*\*|__)(\S(?:.*?\S)?)(?:\*\*|__)(?!\w)", re.S)
_ITALIC = re.compile(r"(?<![\w*_])[*_](\S(?:.*?\S)?)[*_](?![\w*_])", re.S)
_HEADING = re.compile(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*$", re.M)


def telegram_html(text: str) -> str:
    """Convert the model's Markdown to the small HTML subset Telegram renders.

    Telegram has no Markdown mode that survives arbitrary model output: its
    MarkdownV2 requires escaping a dozen characters and rejects the whole
    message on a single stray one. HTML is escaped once, up front, so the worst
    case is a literal asterisk rather than a message that fails to send.
    """
    slots: list[str] = []

    def _stash(html: str) -> str:
        slots.append(html)
        return f"\x00{len(slots) - 1}\x00"

    def _fence(m: re.Match[str]) -> str:
        return _stash(f"<pre><code>{_esc(m.group(2).rstrip())}</code></pre>")

    def _code(m: re.Match[str]) -> str:
        return _stash(f"<code>{_esc(m.group(1))}</code>")

    # Code is stashed before escaping so its contents are never treated as markup.
    out = _FENCE.sub(_fence, text)
    out = _INLINE_CODE.sub(_code, out)
    out = _esc(out)
    out = _HEADING.sub(r"<b>\1</b>", out)
    out = _LINK.sub(lambda m: f'<a href="{m.group(2)}">{m.group(1)}</a>', out)
    out = _BOLD.sub(r"<b>\1</b>", out)
    out = _ITALIC.sub(r"<i>\1</i>", out)
    for i, html in enumerate(slots):
        out = out.replace(f"\x00{i}\x00", html)
    return out


def _esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def split_message(text: str, limit: int = 4096) -> list[str]:
    """Split for Telegram's per-message limit, preferring line boundaries.

    The previous behaviour truncated at the limit, so a long answer lost its
    tail with no indication that anything was missing.
    """
    if len(text) <= limit:
        return [text] if text else []
    parts: list[str] = []
    rest = text
    while len(rest) > limit:
        cut = rest.rfind("\n", 0, limit)
        if cut <= 0:
            cut = rest.rfind(" ", 0, limit)
        if cut <= 0:
            cut = limit
        parts.append(rest[:cut].rstrip())
        rest = rest[cut:].lstrip("\n")
    if rest:
        parts.append(rest)
    return parts


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
