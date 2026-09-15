from __future__ import annotations

import argparse
import asyncio
import json
import os
import socket
import sys
import tempfile
import time
from contextlib import asynccontextmanager
from pathlib import Path
from typing import AsyncIterator

from aiohttp import web

# Codex starts the configured MCP server during thread_start but does not wait
# for the child process, so the one-shot capability is unlinked a short moment
# after the RPC returns (~0.4s on the deployment this was measured against).
# Generous enough to absorb a slow host, short enough that deferring MCP startup
# to first tool use cannot pass.
EAGER_MCP_STARTUP_TIMEOUT = 10.0


def _free_socket() -> socket.socket:
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("127.0.0.1", 0))
    sock.listen(128)
    sock.setblocking(False)
    return sock


@asynccontextmanager
async def _stub_guard(cap: str) -> AsyncIterator[str]:
    """Run a tiny local guard compatible with app.mcp_bridge."""

    async def tools(request: web.Request) -> web.Response:
        if request.match_info["cap"] != cap:
            return web.json_response({"error": "bad cap"}, status=401)
        return web.json_response({
            "tools": [{
                "name": "echo",
                "description": "echo smoke input",
                "inputSchema": {
                    "type": "object",
                    "properties": {"text": {"type": "string"}},
                    "required": ["text"],
                    "additionalProperties": False,
                },
            }]
        })

    async def call(request: web.Request) -> web.Response:
        if request.match_info["cap"] != cap:
            return web.json_response({"error": "bad cap"}, status=401)
        body = await request.json()
        return web.json_response({"result": {"echo": body.get("arguments", {})}})

    app = web.Application()
    app.add_routes([
        web.post("/guard/{cap}/tools", tools),
        web.post("/guard/{cap}/call", call),
    ])
    runner = web.AppRunner(app)
    await runner.setup()
    sock = _free_socket()
    port = sock.getsockname()[1]
    site = web.SockSite(runner, sock)
    await site.start()
    try:
        yield f"http://127.0.0.1:{port}"
    finally:
        await runner.cleanup()


async def smoke_bridge() -> None:
    """Exercise the real MCP v2 client -> stdio bridge -> stub guard path."""
    from mcp import Client, StdioServerParameters

    cap = "smoke-capability-0123456789abcdef0123456789abcdef"
    async with _stub_guard(cap) as guard_url:
        with tempfile.TemporaryDirectory(prefix="calendar-bot-smoke-") as td:
            cap_file = Path(td) / "cap"
            cap_file.write_text(cap, encoding="utf-8")
            os.chmod(cap_file, 0o600)
            bridge = Path(__file__).resolve().with_name("mcp_bridge.py")
            params = StdioServerParameters(
                command=sys.executable,
                args=[
                    str(bridge),
                    "--guard", guard_url,
                    "--cap-file", str(cap_file),
                ],
            )
            async with Client(params) as client:
                listed = await client.list_tools()
                assert [tool.name for tool in listed.tools] == ["echo"]
                result = await client.call_tool("echo", {"text": "ok"})
                assert not result.is_error
                assert result.structured_content == {"echo": {"text": "ok"}}
                assert not cap_file.exists(), "bridge did not unlink one-shot capability"


def _codex_env(home: Path) -> dict[str, str]:
    """Minimal smoke environment, preserving explicitly configured Codex proxies."""
    env = {
        "CODEX_HOME": str(home),
        "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
        "HOME": os.environ.get("HOME", str(home.parent)),
        "NO_PROXY": os.environ.get("NO_PROXY", "127.0.0.1,localhost"),
        "no_proxy": os.environ.get("NO_PROXY", "127.0.0.1,localhost"),
    }
    mappings = {
        "CODEX_HTTP_PROXY": ("HTTP_PROXY", "http_proxy"),
        "CODEX_HTTPS_PROXY": ("HTTPS_PROXY", "https_proxy"),
        "CODEX_ALL_PROXY": ("ALL_PROXY", "all_proxy"),
    }
    for source, targets in mappings.items():
        value = os.environ.get(source)
        if value:
            for target in targets:
                env[target] = value
    return env


async def _codex_initialize(home: Path) -> None:
    from openai_codex import AsyncCodex, CodexConfig

    async with AsyncCodex(CodexConfig(env=_codex_env(home))) as codex:
        # account/read exercises app-server initialization without a model turn.
        await codex.account(refresh_token=False)


async def _codex_thread_start(home: Path, cwd: Path) -> None:
    """Start, but do not run, an ephemeral Codex thread."""
    from openai_codex import ApprovalMode, AsyncCodex, CodexConfig

    async with AsyncCodex(CodexConfig(env=_codex_env(home))) as codex:
        await codex.thread_start(
            approval_mode=ApprovalMode.deny_all,
            developer_instructions="runtime smoke; do not execute a model turn",
            ephemeral=True,
            cwd=str(cwd),
            config={"web_search": "disabled"},
        )


def _q(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def _write_good_mcp_config(home: Path, workdir: Path, guard_url: str, cap_file: Path) -> None:
    bridge = Path(__file__).resolve().with_name("mcp_bridge.py")
    args = [
        str(bridge),
        "--guard", guard_url,
        "--cap-file", str(cap_file),
    ]
    (home / "config.toml").write_text(
        "\n".join([
            'check_for_update_on_startup = false',
            'allow_login_shell = false',
            'default_permissions = "calendar_bot"',
            '',
            '[permissions.calendar_bot]',
            'extends = ":read-only"',
            '',
            '[permissions.calendar_bot.filesystem]',
            '":root" = "deny"',
            '":minimal" = "read"',
            f'{_q(str(workdir.resolve()))} = "read"',
            '',
            '[permissions.calendar_bot.network]',
            'enabled = false',
            '',
            '[mcp_servers.calendar]',
            f'command = {_q(sys.executable)}',
            'args = [' + ', '.join(_q(item) for item in args) + ']',
            'startup_timeout_sec = 10',
            'tool_timeout_sec = 30',
            '',
        ]),
        encoding="utf-8",
    )


async def _expect_config_rejection(home: Path, cwd: Path, markers: tuple[str, ...], description: str) -> None:
    try:
        await _codex_thread_start(home, cwd)
    except Exception as exc:
        text = str(exc).lower()
        if not any(marker.lower() in text for marker in markers):
            raise RuntimeError(
                f"Codex failed, but not because {description} was rejected: {exc}"
            ) from exc
    else:
        raise RuntimeError(f"Codex accepted {description}; refusing to trust sandbox config")


async def smoke_codex_config() -> None:
    """Validate permission profiles and prove MCP is eagerly started by thread_start.

    This is intentionally a runtime/deployment smoke rather than a docker-build step:
    it exercises the installed Codex app-server and may depend on the host's proxy/runtime.
    """
    with tempfile.TemporaryDirectory(prefix="codex-config-smoke-") as td:
        root = Path(td)

        # First prove the app-server itself initializes in this environment so a
        # generic runtime failure is not mistaken for a successful config rejection.
        init_home = root / "initialize"
        init_home.mkdir()
        (init_home / "config.toml").write_text(
            'check_for_update_on_startup = false\n'
            'default_permissions = ":read-only"\n',
            encoding="utf-8",
        )
        await _codex_initialize(init_home)

        # Critical one-shot assumption: Codex must launch/initialize configured MCP
        # servers during thread_start. If MCP startup becomes lazy/asynchronous in a
        # future runtime, this fails before deployment rather than every bot request.
        good_home = root / "good-mcp"
        good_home.mkdir()
        workdir = root / "work"
        workdir.mkdir()
        cap = "smoke-capability-eager-mcp-0123456789abcdef01234567"
        cap_file = root / "eager.cap"
        cap_file.write_text(cap, encoding="utf-8")
        os.chmod(cap_file, 0o600)
        async with _stub_guard(cap) as guard_url:
            _write_good_mcp_config(good_home, workdir, guard_url, cap_file)
            await _codex_thread_start(good_home, workdir)
            # thread_start launches the MCP server itself, but does not block
            # until the child has started, so the unlink lands shortly after the
            # call returns rather than before it. Poll for it: no tool call is
            # ever made here, so a runtime that defers MCP startup until first
            # use still never consumes the capability and still fails below.
            deadline = time.monotonic() + EAGER_MCP_STARTUP_TIMEOUT
            while cap_file.exists() and time.monotonic() < deadline:
                await asyncio.sleep(0.05)
        if cap_file.exists():
            raise RuntimeError(
                "the configured MCP bridge did not consume its capability within "
                f"{EAGER_MCP_STARTUP_TIMEOUT:.0f}s of thread_start and without any tool call; "
                "MCP startup is not eager and one-shot capability semantics are unsafe "
                "with this runtime"
            )

        # Profile-name validation.
        bad_name = root / "bad-name"
        bad_name.mkdir()
        (bad_name / "config.toml").write_text(
            'check_for_update_on_startup = false\n'
            'default_permissions = "does_not_exist"\n',
            encoding="utf-8",
        )
        await _expect_config_rejection(
            bad_name,
            workdir,
            ("does_not_exist", "undefined profile", "default_permissions", "permission"),
            "bogus default_permissions=does_not_exist",
        )

        # Inner-profile validation: proving only the profile name is resolved is not
        # enough. A bogus `extends` must also be rejected by the installed runtime.
        bad_inner = root / "bad-inner"
        bad_inner.mkdir()
        (bad_inner / "config.toml").write_text(
            'check_for_update_on_startup = false\n'
            'default_permissions = "broken"\n\n'
            '[permissions.broken]\n'
            'extends = ":does_not_exist"\n',
            encoding="utf-8",
        )
        await _expect_config_rejection(
            bad_inner,
            workdir,
            (":does_not_exist", "extends", "permission", "profile"),
            "bogus inner permission profile extends=:does_not_exist",
        )


async def main_async(mode: str) -> None:
    if mode in {"all", "bridge"}:
        await smoke_bridge()
    if mode in {"all", "codex-config"}:
        await smoke_codex_config()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["all", "bridge", "codex-config"], default="all")
    args = parser.parse_args()
    asyncio.run(main_async(args.mode))


if __name__ == "__main__":
    main()
