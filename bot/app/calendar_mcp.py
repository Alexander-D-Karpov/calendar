from __future__ import annotations

import asyncio
import hashlib
import time
from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

import httpx2
from mcp import Client
from mcp.client.streamable_http import streamable_http_client

from .utils import normalize_tool_name, result_to_jsonish


class CalendarMCP:
    def __init__(self, url: str, proxy: str | None = None, tool_cache_ttl: int = 300) -> None:
        self.url = url
        self.proxy = proxy
        self.tool_cache_ttl = max(1, tool_cache_ttl)
        self._tool_cache: dict[str, tuple[float, list[dict[str, Any]]]] = {}
        self._tool_locks: dict[str, asyncio.Lock] = {}

    @staticmethod
    def _token_key(token: str) -> str:
        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    def _prune_tool_cache(self, now: float | None = None) -> None:
        now = time.monotonic() if now is None else now
        expired = [key for key, (deadline, _) in self._tool_cache.items() if deadline <= now]
        for key in expired:
            self._tool_cache.pop(key, None)
            lock = self._tool_locks.get(key)
            if lock is not None and not lock.locked():
                self._tool_locks.pop(key, None)

    @asynccontextmanager
    async def client(self, token: str) -> AsyncIterator[Client]:
        kwargs: dict[str, Any] = {
            "headers": {"Authorization": f"Bearer {token}"},
            "timeout": httpx2.Timeout(300.0, connect=30.0),
        }
        if self.proxy:
            kwargs["proxy"] = self.proxy
        async with httpx2.AsyncClient(**kwargs) as http:
            transport = streamable_http_client(self.url, http_client=http)
            async with Client(transport) as client:
                yield client

    async def list_tools(self, token: str, *, force: bool = False) -> list[dict[str, Any]]:
        key = self._token_key(token)
        now = time.monotonic()
        self._prune_tool_cache(now)
        cached = self._tool_cache.get(key)
        if not force and cached and cached[0] > now:
            return list(cached[1])

        # Lock only this token: one slow user's schema fetch must not serialize all
        # other users' first requests/poller ticks.
        lock = self._tool_locks.setdefault(key, asyncio.Lock())
        async with lock:
            now = time.monotonic()
            cached = self._tool_cache.get(key)
            if not force and cached and cached[0] > now:
                return list(cached[1])
            async with self.client(token) as client:
                result = await client.list_tools()
                tools = [t.model_dump(by_alias=True, exclude_none=True) for t in result.tools]
            self._tool_cache[key] = (time.monotonic() + self.tool_cache_ttl, tools)
            return list(tools)

    async def resolve_tool_name(self, token: str, logical_name: str) -> str | None:
        wanted = normalize_tool_name(logical_name)
        tools = await self.list_tools(token)
        exact: list[str] = []
        suffix: list[str] = []
        for tool in tools:
            name = str(tool.get("name") or "")
            normalized = normalize_tool_name(name)
            if normalized == wanted:
                exact.append(name)
            elif normalized.endswith(wanted):
                suffix.append(name)
        if len(exact) == 1:
            return exact[0]
        if not exact and len(suffix) == 1:
            return suffix[0]
        return None

    async def call(self, token: str, name: str, arguments: dict[str, Any] | None = None) -> Any:
        async with self.client(token) as client:
            result = await client.call_tool(name, arguments or {})
            if getattr(result, "is_error", False):
                value = result_to_jsonish(result)
                raise RuntimeError(f"Calendar MCP tool {name} failed: {value}")
            value = result_to_jsonish(result)
            while isinstance(value, dict) and set(value) == {"result"}:
                value = value["result"]
            return value
