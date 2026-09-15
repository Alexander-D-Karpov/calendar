from __future__ import annotations

import json
import os
import shutil
import sys
import uuid
from pathlib import Path
from typing import Any

from openai_codex import (
    ApprovalMode,
    AsyncCodex,
    CodexConfig,
    LocalImageInput,
    TextInput,
)

from .config import Settings
from .utils import parse_agent_response


RESULT_SCHEMA: dict[str, Any] = {
    "type": "object",
    "properties": {
        "status": {"type": "string", "enum": ["ask", "done"]},
        "text": {"type": "string"},
        "refs": {"type": "array", "items": {"type": "string"}, "maxItems": 20},
    },
    "required": ["status", "text", "refs"],
    "additionalProperties": False,
}


class CodexRuntime:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.bridge_path = Path(__file__).resolve().with_name("mcp_bridge.py")

    def home(self, user_id: int) -> Path:
        p = self.settings.codex_dir / str(user_id)
        p.mkdir(parents=True, exist_ok=True)
        try:
            os.chmod(p, 0o700)
        except PermissionError:
            pass
        return p

    def auth_path(self, user_id: int) -> Path:
        return self.home(user_id) / "auth.json"

    def _env(self, user_id: int) -> dict[str, str]:
        # Never inherit the bot process environment wholesale. It contains the
        # Telegram token, state-encryption key and possibly deployment secrets.
        allowed = {
            "PATH", "HOME", "USER", "LOGNAME", "LANG", "LANGUAGE",
            "LC_ALL", "LC_CTYPE", "TERM", "TMPDIR", "TZ",
            "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE",
        }
        env = {key: value for key, value in os.environ.items() if key in allowed}
        env["CODEX_HOME"] = str(self.home(user_id))
        env["NO_PROXY"] = self.settings.no_proxy
        env["no_proxy"] = self.settings.no_proxy
        if self.settings.codex_http_proxy:
            env["HTTP_PROXY"] = self.settings.codex_http_proxy
            env["http_proxy"] = self.settings.codex_http_proxy
        if self.settings.codex_https_proxy:
            env["HTTPS_PROXY"] = self.settings.codex_https_proxy
            env["https_proxy"] = self.settings.codex_https_proxy
        if self.settings.codex_all_proxy:
            env["ALL_PROXY"] = self.settings.codex_all_proxy
            env["all_proxy"] = self.settings.codex_all_proxy
        return env

    @staticmethod
    def _toml_string(value: str) -> str:
        return json.dumps(value, ensure_ascii=False)

    def _write_cap_file(self, user_id: int, cap: str) -> Path:
        """Create a one-shot capability file for the stdio MCP bridge.

        The secret is deliberately absent from argv, config.toml and the Codex
        environment. The bridge reads and unlinks this file before serving MCP.
        """
        self.settings.cap_dir.mkdir(parents=True, exist_ok=True)
        name = f"{user_id}-{uuid.uuid4().hex}.cap"
        path = self.settings.cap_dir / name
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                f.write(cap)
                f.flush()
        except Exception:
            path.unlink(missing_ok=True)
            raise
        return path

    def write_config(
        self,
        user_id: int,
        *,
        guard_url: str | None = None,
        cap_file: Path | None = None,
        workdir: Path | None = None,
    ) -> Path:
        # All top-level keys must appear before any TOML table header.
        lines = [
            'cli_auth_credentials_store = "file"',
            'history.persistence = "none"',
            'web_search = "disabled"',
            'check_for_update_on_startup = false',
            'allow_login_shell = false',
        ]
        if workdir is not None:
            lines.append('default_permissions = "calendar_bot"')

        lines += [
            '',
            '[shell_environment_policy]',
            'inherit = "core"',
            'ignore_default_excludes = false',
            '',
        ]

        if workdir is not None:
            lines += [
                '[permissions.calendar_bot]',
                'extends = ":read-only"',
                '',
                '[permissions.calendar_bot.filesystem]',
                '":root" = "deny"',
                '":minimal" = "read"',
                f'{self._toml_string(str(workdir.resolve()))} = "read"',
                '',
                '[permissions.calendar_bot.network]',
                'enabled = false',
                '',
            ]
        if guard_url and cap_file:
            args = [
                str(self.bridge_path),
                "--guard", guard_url,
                "--cap-file", str(cap_file.resolve()),
            ]
            lines += [
                '[mcp_servers.calendar]',
                f'command = {self._toml_string(sys.executable)}',
                'args = [' + ', '.join(self._toml_string(x) for x in args) + ']',
                'startup_timeout_sec = 10',
                'tool_timeout_sec = 120',
                '',
            ]
        path = self.home(user_id) / "config.toml"
        tmp = path.with_suffix(".tmp")
        tmp.write_text("\n".join(lines), encoding="utf-8")
        os.replace(tmp, path)
        try:
            os.chmod(path, 0o600)
        except PermissionError:
            pass
        return path

    def client(self, user_id: int) -> AsyncCodex:
        return AsyncCodex(CodexConfig(env=self._env(user_id)))

    async def account(self, user_id: int) -> Any:
        # A status check must not rewrite a request-owned config.toml.
        if not (self.home(user_id) / "config.toml").exists():
            self.write_config(user_id)
        async with self.client(user_id) as codex:
            return await codex.account(refresh_token=False)

    async def logout(self, user_id: int) -> None:
        self.write_config(user_id)
        async with self.client(user_id) as codex:
            await codex.logout()

    async def start_device_login(self, user_id: int) -> tuple[AsyncCodex, Any]:
        self.write_config(user_id)
        codex = self.client(user_id)
        await codex.__aenter__()
        try:
            handle = await codex.login_chatgpt_device_code()
            return codex, handle
        except BaseException:
            await codex.close()
            raise

    def import_auth_json(self, user_id: int, source: Path) -> None:
        if source.stat().st_size > 2 * 1024 * 1024:
            raise ValueError("auth.json is unexpectedly large")
        data = json.loads(source.read_text(encoding="utf-8"))
        if not isinstance(data, dict) or not data:
            raise ValueError("auth.json must contain a JSON object")
        dest = self.auth_path(user_id)
        tmp = dest.with_suffix(".tmp")
        shutil.copyfile(source, tmp)
        os.chmod(tmp, 0o600)
        os.replace(tmp, dest)

    async def new_session(
        self,
        user_id: int,
        workdir: Path,
        guard_url: str,
        cap: str,
        timezone: str,
        now_text: str,
    ) -> "LiveCodexSession":
        cap_file = self._write_cap_file(user_id, cap)
        self.write_config(user_id, guard_url=guard_url, cap_file=cap_file, workdir=workdir)
        client = self.client(user_id)
        await client.__aenter__()
        developer = self._developer_prompt(timezone, now_text)
        kwargs: dict[str, Any] = {
            "approval_mode": ApprovalMode.deny_all,
            "developer_instructions": developer,
            "ephemeral": True,
            "cwd": str(workdir),
            # Permission profiles are selected through config. Do not also pass the
            # legacy sandbox preset: current Codex treats those systems as mutually
            # exclusive.
            "config": {
                "web_search": "disabled",
                "model_reasoning_effort": self.settings.codex_reasoning_effort,
            },
        }
        if self.settings.codex_model:
            kwargs["model"] = self.settings.codex_model
        try:
            thread = await client.thread_start(**kwargs)
            # MCP startup happens as part of Codex initialization. The bridge must
            # have consumed/unlinked the capability before a model turn can run.
            if cap_file.exists():
                raise RuntimeError("calendar MCP bridge did not consume its one-shot capability file")
        except BaseException:
            cap_file.unlink(missing_ok=True)
            await client.close()
            raise
        return LiveCodexSession(client, thread, workdir)

    @staticmethod
    def _developer_prompt(timezone: str, now_text: str) -> str:
        return f"""You are a private calendar/todo assistant. Current user timezone: {timezone}. Current local time: {now_text}.
Use the calendar MCP tools for calendar/todo facts and mutations. Do not guess IDs, calendar names, dates, or existing state.
Ask the user a concise question only when a material ambiguity prevents a safe action. Otherwise complete the request.
For destructive operations, the MCP guard is authoritative. Never try to bypass it. A request may delete at most one logical entry. Safe delete targets are one event occurrence, one todo with no subtasks, or one checklist item. Never delete a calendar, todo list, recurring series, subscription, or cascading subtree. For recurring event deletion, one occurrence means scope=this with the exact instance. Recurring event updates may use this/following/all when that matches the user's request.
Creating public shares, creating/importing bulk data, adding remote subscriptions, duplicate resolution, and account-setting writes are intentionally unavailable. Revoking an existing public share is allowed.
Files mentioned in the user turn are in the current working directory. Inspect only files needed for the task. Do not copy their full contents into the response.
Do not search the web. When the request explicitly depends on an earlier completed request or durable memory, call search_bot_context; otherwise do not call it.
Keep tool use and output compact. End with the structured response required by the output schema: status=ask when a user answer is needed, status=done when complete. text is the Telegram reply; refs contains useful event/todo IDs only when relevant."""


class LiveCodexSession:
    def __init__(self, client: AsyncCodex, thread: Any, workdir: Path) -> None:
        self.client = client
        self.thread = thread
        self.workdir = workdir

    async def run(self, text: str, image_paths: list[Path] | None = None) -> dict[str, Any]:
        inputs: list[Any] = [TextInput(text=text)]
        for path in image_paths or []:
            inputs.append(LocalImageInput(path=str(path)))
        result = await self.thread.run(
            inputs,
            output_schema=RESULT_SCHEMA,
            approval_mode=ApprovalMode.deny_all,
        )
        return parse_agent_response(result.final_response)

    async def close(self) -> None:
        await self.client.close()
