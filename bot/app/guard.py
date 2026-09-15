from __future__ import annotations

import asyncio
import logging
import re
import secrets
import time
from dataclasses import dataclass, field
from typing import Any, TYPE_CHECKING

from aiohttp import web

if TYPE_CHECKING:
    from .calendar_mcp import CalendarMCP
from .store import Store
from .utils import normalize_tool_name

log = logging.getLogger(__name__)


# Operations outside the intended calendar/todo assistant role or with an
# unbounded blast radius. Narrow ACalendar token scopes remain the first layer.
BLOCK_SUBSTRINGS = (
    "deletecalendar",
    "deletetodolist",
    "purge",
    "emptytrash",
    "mergeduplicate",
    "resolveduplicate",
    "bulkdelete",
    "deleteall",
)
BLOCK_WRITE_NAMES = {
    "updateme",
    "createshare",
    "updateshare",
    "regenerateshare",
    # revokeShare is intentionally allowed: it reduces public exposure.
    "createimport",
    "getimport",
    "commitimport",
    "scanduplicates",
    "resolveduplicate",
    # Remote ICS subscriptions can inject an unbounded number of entries.
    "createsubscription",
    "updatesubscription",
    "refreshsubscription",
}
ALLOWED_DELETE_NAMES = {"deleteevent", "deletetodo", "deletecheck"}

SEARCH_CONTEXT_TOOL = {
    "name": "search_bot_context",
    "description": "Search compact completed-request summaries and explicit user memories. Use only when prior context is actually needed.",
    "inputSchema": {
        "type": "object",
        "properties": {
            "query": {"type": "string"},
            "limit": {"type": "integer", "minimum": 1, "maximum": 10, "default": 5},
        },
        "required": ["query"],
        "additionalProperties": False,
    },
}


@dataclass
class Capability:
    user_id: int
    token: str
    tools: list[dict[str, Any]]
    expires_at: float
    delete_count: int = 0
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)


class GuardServer:
    def __init__(self, calendar: "CalendarMCP", store: Store, host: str, port: int, ttl: int) -> None:
        self.calendar = calendar
        self.store = store
        self.host = host
        self.port = port
        self.ttl = ttl
        self._caps: dict[str, Capability] = {}
        self._runner: web.AppRunner | None = None

    @property
    def base_url(self) -> str:
        return f"http://{self.host}:{self.port}"

    async def start(self) -> None:
        app = web.Application(client_max_size=2 * 1024 * 1024)
        app.add_routes([
            web.get("/health", self._health),
            web.post("/guard/{cap}/tools", self._tools),
            web.post("/guard/{cap}/call", self._call),
        ])
        self._runner = web.AppRunner(app)
        await self._runner.setup()
        await web.TCPSite(self._runner, self.host, self.port).start()

    async def stop(self) -> None:
        if self._runner:
            await self._runner.cleanup()
        self._caps.clear()

    async def create_capability(self, user_id: int, token: str) -> str:
        tools = await self.calendar.list_tools(token)
        cap = secrets.token_urlsafe(32)
        self._caps[cap] = Capability(
            user_id=user_id,
            token=token,
            tools=tools,
            expires_at=time.monotonic() + self.ttl,
        )
        self._gc()
        return cap

    def touch(self, cap: str) -> bool:
        item = self._caps.get(cap)
        if item is None:
            return False
        now = time.monotonic()
        if item.expires_at < now:
            self._caps.pop(cap, None)
            return False
        item.expires_at = now + self.ttl
        return True

    def revoke(self, cap: str | None) -> None:
        if cap:
            self._caps.pop(cap, None)

    def _gc(self) -> None:
        now = time.monotonic()
        for key in [k for k, v in self._caps.items() if v.expires_at < now]:
            self._caps.pop(key, None)

    def _get(self, key: str) -> Capability:
        self._gc()
        cap = self._caps.get(key)
        if not cap or cap.expires_at < time.monotonic():
            raise web.HTTPUnauthorized(text="expired or invalid capability")
        cap.expires_at = time.monotonic() + self.ttl
        return cap

    async def _health(self, _: web.Request) -> web.Response:
        return web.json_response({"ok": True})

    @staticmethod
    def _delete_kind(normalized: str) -> str | None:
        for kind in ALLOWED_DELETE_NAMES:
            if normalized.endswith(kind):
                return kind
        return None

    @classmethod
    def _safe_tool(cls, tool: dict[str, Any]) -> bool:
        n = normalize_tool_name(str(tool.get("name", "")))
        if any(normalize_tool_name(x) in n for x in BLOCK_SUBSTRINGS):
            return False
        if any(n == blocked or n.endswith(blocked) for blocked in BLOCK_WRITE_NAMES):
            return False
        if "delete" in n and cls._delete_kind(n) is None:
            return False
        return True

    async def _tools(self, request: web.Request) -> web.Response:
        cap = self._get(request.match_info["cap"])
        tools = [t for t in cap.tools if self._safe_tool(t)]
        tools.append(SEARCH_CONTEXT_TOOL)
        return web.json_response({"tools": tools})

    def _tool_name(self, cap: Capability, normalized: str) -> str | None:
        matches: list[str] = []
        for tool in cap.tools:
            actual = normalize_tool_name(str(tool.get("name", "")))
            if actual == normalized:
                return str(tool["name"])
            if actual.endswith(normalized):
                matches.append(str(tool["name"]))
        return matches[0] if len(matches) == 1 else None

    @staticmethod
    def _extract_id(arguments: dict[str, Any], kind: str) -> str | None:
        keys = ["id", f"{kind}_id", f"{kind}Id"]
        for key in keys:
            value = arguments.get(key)
            if isinstance(value, str) and value:
                return value
        return None

    async def _preflight_event_delete(self, cap: Capability, arguments: dict[str, Any]) -> None:
        scope = arguments.get("scope")
        if scope in {"all", "following"}:
            raise ValueError(f"Blocked: deletion may affect only one event occurrence; scope={scope} is not allowed")
        event_id = self._extract_id(arguments, "event")
        if not event_id:
            raise ValueError("Blocked: deletion is missing an event id")
        getter = self._tool_name(cap, "getevent")
        if not getter:
            if scope != "this" or not arguments.get("instance"):
                raise ValueError("Blocked: cannot prove deletion affects only one event occurrence")
            return
        event = await self.calendar.call(cap.token, getter, {"id": event_id})
        if isinstance(event, dict) and event.get("recurring"):
            if scope != "this" or not arguments.get("instance"):
                raise ValueError("Blocked: recurring event deletion must use scope=this and an explicit instance")

    async def _preflight_todo_delete(self, cap: Capability, arguments: dict[str, Any]) -> None:
        todo_id = self._extract_id(arguments, "todo")
        if not todo_id:
            raise ValueError("Blocked: deleteTodo is missing a todo id")
        list_name = self._tool_name(cap, "listtodos")
        if not list_name:
            raise ValueError("Blocked: cannot verify that the todo has no subtasks")
        children = await self.calendar.call(cap.token, list_name, {"parent_id": todo_id, "status": "all", "limit": 1})
        items: list[Any] = []
        if isinstance(children, dict):
            maybe = children.get("items")
            if isinstance(maybe, list):
                items = maybe
        if items:
            raise ValueError("Blocked: deleting this todo would also delete subtasks (>1 entry)")

    @staticmethod
    def _safe_error(exc: Exception, token: str) -> str:
        # Keep actionable validation text while preventing a future upstream error
        # formatter from echoing credentials into the model context.
        text = str(exc).replace(token, "[redacted]")
        text = re.sub(r"(?i)bearer\s+[A-Za-z0-9._~+/=-]+", "Bearer [redacted]", text)
        text = re.sub(r"\bcal_[A-Za-z0-9_-]{8,}\b", "cal_[redacted]", text)
        text = re.sub(r"(?i)(authorization\s*[:=]\s*)[^\s,;]+", r"\1[redacted]", text)
        return text[:1200] or "calendar operation failed"

    async def _call(self, request: web.Request) -> web.Response:
        cap = self._get(request.match_info["cap"])
        try:
            body = await request.json()
        except Exception:
            return web.json_response({"error": "request body must be valid JSON"}, status=400)
        if not isinstance(body, dict):
            return web.json_response({"error": "request body must be an object"}, status=400)

        name = str(body.get("name") or "")
        arguments = body.get("arguments") or {}
        if not isinstance(arguments, dict):
            return web.json_response({"error": "arguments must be an object"}, status=400)

        if name == "search_bot_context":
            query = str(arguments.get("query") or "")
            try:
                limit = max(1, min(int(arguments.get("limit") or 5), 10))
            except (TypeError, ValueError):
                return web.json_response({"error": "limit must be an integer"}, status=400)
            result = self.store.search_context(cap.user_id, query, limit)
            return web.json_response({"result": result})

        tool = next((t for t in cap.tools if t.get("name") == name), None)
        if not tool or not self._safe_tool(tool):
            return web.json_response({"error": "tool is not available through the guard"}, status=403)

        normalized = normalize_tool_name(name)
        delete_kind = self._delete_kind(normalized)
        async with cap.lock:
            try:
                # Updates are intentionally not subject to the one-delete rule.
                # Recurring updates may use this/following/all when requested.
                if delete_kind:
                    if cap.delete_count >= 1:
                        raise ValueError("Blocked: at most one logical entry may be deleted per request")
                    if delete_kind == "deleteevent":
                        await self._preflight_event_delete(cap, arguments)
                    elif delete_kind == "deletetodo":
                        await self._preflight_todo_delete(cap, arguments)
                    # Reserve before upstream: even a failed destructive attempt
                    # consumes this request's destructive budget.
                    cap.delete_count += 1
                result = await self.calendar.call(cap.token, name, arguments)
            except Exception as exc:
                log.warning("guarded calendar tool %s failed for user %s", name, cap.user_id, exc_info=True)
                return web.json_response({"error": self._safe_error(exc, cap.token)}, status=400)
        return web.json_response({"result": result})
