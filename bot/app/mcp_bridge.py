"""Request-scoped stdio MCP bridge exposed to Codex.

The bridge contains no ACalendar bearer token. It receives a path to a one-shot
capability file, reads it, and unlinks the file before serving MCP. The secret is
therefore absent from argv, config.toml and the Codex process environment.
"""
from __future__ import annotations

import argparse
import asyncio
import json
from pathlib import Path
from typing import Any

import aiohttp


class GuardProxy:
    def __init__(self, guard: str, cap: str) -> None:
        self.guard = guard.rstrip("/")
        self.cap = cap
        self.session: aiohttp.ClientSession | None = None

    async def __aenter__(self) -> "GuardProxy":
        self.session = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=180))
        return self

    async def __aexit__(self, *_: object) -> None:
        if self.session:
            await self.session.close()
            self.session = None

    async def post(self, path: str, body: dict[str, Any]) -> tuple[int, Any]:
        if self.session is None:
            raise RuntimeError("guard proxy is not started")
        async with self.session.post(f"{self.guard}/guard/{self.cap}/{path}", json=body) as resp:
            try:
                data = await resp.json()
            except Exception:
                data = {"error": await resp.text()}
            return resp.status, data

    async def tools(self) -> list[dict[str, Any]]:
        status, data = await self.post("tools", {})
        if status >= 400:
            raise RuntimeError(str(data.get("error") or "guard rejected tools/list"))
        tools = data.get("tools") or []
        if not isinstance(tools, list):
            raise RuntimeError("guard returned an invalid tools list")
        return [x for x in tools if isinstance(x, dict)]

    async def call(self, name: str, arguments: dict[str, Any]) -> tuple[bool, Any]:
        status, data = await self.post("call", {"name": name, "arguments": arguments})
        if status >= 400:
            return False, str(data.get("error") or "guard rejected the call")
        return True, data.get("result")


def read_one_shot_capability(path: Path) -> str:
    """Read and unlink a request capability before MCP starts accepting calls."""
    try:
        cap = path.read_text(encoding="utf-8").strip()
    finally:
        path.unlink(missing_ok=True)
    if len(cap) < 32 or len(cap) > 256 or any(ch.isspace() for ch in cap):
        raise RuntimeError("invalid capability file")
    return cap


async def run_server(guard: str, cap: str) -> None:
    # Keep SDK imports inside runtime entry so light unit tests do not need the
    # optional MCP dependency installed in the packaging harness.
    from mcp.server import Server, ServerRequestContext
    from mcp.server.stdio import stdio_server
    from mcp.types import (
        CallToolRequestParams,
        CallToolResult,
        ListToolsResult,
        PaginatedRequestParams,
        TextContent,
        Tool,
    )

    async with GuardProxy(guard, cap) as proxy:
        async def list_tools(
            ctx: ServerRequestContext,
            params: PaginatedRequestParams | None,
        ) -> ListToolsResult:
            del ctx, params
            return ListToolsResult(tools=[Tool.model_validate(t) for t in await proxy.tools()])

        async def call_tool(
            ctx: ServerRequestContext,
            params: CallToolRequestParams,
        ) -> CallToolResult:
            del ctx
            ok, value = await proxy.call(params.name, params.arguments or {})
            if not ok:
                return CallToolResult(
                    content=[TextContent(type="text", text=str(value))],
                    is_error=True,
                )

            text = json.dumps(value, ensure_ascii=False, default=str)
            kwargs: dict[str, Any] = {
                "content": [TextContent(type="text", text=text)],
                "is_error": False,
            }
            if isinstance(value, dict):
                kwargs["structured_content"] = value
            return CallToolResult(**kwargs)

        server = Server(
            "calendar-guard",
            version="0.4.0",
            on_list_tools=list_tools,
            on_call_tool=call_tool,
        )
        async with stdio_server() as (read_stream, write_stream):
            await server.run(
                read_stream,
                write_stream,
                server.create_initialization_options(),
            )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--guard", required=True)
    parser.add_argument("--cap-file", required=True, type=Path)
    args = parser.parse_args()
    cap = read_one_shot_capability(args.cap_file)
    asyncio.run(run_server(args.guard, cap))


if __name__ == "__main__":
    main()
