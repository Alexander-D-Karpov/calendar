from __future__ import annotations

import asyncio
import shutil
import uuid
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

from aiogram import Bot, Router
from aiogram.enums import ChatType
from aiogram.filters import Command, CommandObject
from aiogram.types import Message

from .calendar_mcp import CalendarMCP
from .codex_runtime import CodexRuntime
from .config import Settings
from .crypto import SecretBox
from .requests import Incoming, RequestManager
from .store import Store
from .utils import safe_filename


HELP = """Send a message (optionally with photos/documents) describing what to do with calendar/todos.

Setup:
/link cal_… — link your ACalendar token
/unlink — remove calendar token
/codex_login — ChatGPT/Codex device login
/codex_session — upload your existing auth.json as the next document
/codex_status — check Codex login
/codex_logout — clear Codex login
/status — show bot setup state
/timezone Europe/Vilnius — set timezone
/poll on|off — external-change polling + morning agenda
/cancel — cancel current request and queued requests
/memory add <text> — durable explicit memory
/memory list
/memory clear

Admin:
/users
/user_add <telegram_id> [user|admin]
/user_role <telegram_id> <user|admin>
/user_del <telegram_id>"""


class Handlers:
    def __init__(
        self,
        settings: Settings,
        store: Store,
        crypto: SecretBox,
        calendar: CalendarMCP,
        codex: CodexRuntime,
        requests: RequestManager,
    ) -> None:
        self.settings = settings
        self.store = store
        self.crypto = crypto
        self.calendar = calendar
        self.codex = codex
        self.requests = requests
        self.router = Router()
        self.pending_auth_upload: set[int] = set()
        self.login_tasks: dict[int, asyncio.Task[None]] = {}
        self.login_handles: dict[int, Any] = {}
        self.albums: dict[str, list[Message]] = {}
        self.album_tasks: dict[str, asyncio.Task[None]] = {}
        self._register()

    async def shutdown(self) -> None:
        tasks = [*self.login_tasks.values(), *self.album_tasks.values()]
        for task in tasks:
            if not task.done():
                task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        self.login_tasks.clear()
        self.album_tasks.clear()
        self.albums.clear()

    def _register(self) -> None:
        r = self.router
        r.message.register(self.start, Command("start", "help"))
        r.message.register(self.status, Command("status"))
        r.message.register(self.link, Command("link"))
        r.message.register(self.unlink, Command("unlink"))
        r.message.register(self.codex_login, Command("codex_login"))
        r.message.register(self.codex_status, Command("codex_status"))
        r.message.register(self.codex_logout, Command("codex_logout"))
        r.message.register(self.codex_session, Command("codex_session"))
        r.message.register(self.timezone, Command("timezone"))
        r.message.register(self.poll, Command("poll"))
        r.message.register(self.cancel, Command("cancel"))
        r.message.register(self.memory, Command("memory"))
        r.message.register(self.users, Command("users"))
        r.message.register(self.user_add, Command("user_add"))
        r.message.register(self.user_role, Command("user_role"))
        r.message.register(self.user_del, Command("user_del"))
        r.message.register(self.catch_all)
        r.edited_message.register(self.edited)

    async def _private_allowed(self, message: Message) -> bool:
        if message.chat.type != ChatType.PRIVATE:
            await message.answer("Use this bot in DM.")
            return False
        if not message.from_user or not self.store.is_allowed(message.from_user.id):
            await message.answer("You are not authorized. Ask the bot admin to add your Telegram user ID.")
            return False
        return True

    async def _admin(self, message: Message) -> bool:
        if not await self._private_allowed(message):
            return False
        assert message.from_user
        if not self.store.is_admin(message.from_user.id):
            await message.answer("Admin only.")
            return False
        return True

    async def start(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        await message.answer(HELP)

    async def status(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        user = self.store.get_user(uid) or {}
        codex_ok = self.codex.auth_path(uid).exists()
        await message.answer(
            f"Role: {user.get('role')}\n"
            f"Calendar: {'linked' if user.get('calendar_token') else 'not linked'}\n"
            f"Codex auth file: {'present' if codex_ok else 'missing'}\n"
            f"Timezone: {user.get('timezone') or self.settings.default_timezone}\n"
            f"Polling: {'on' if user.get('poll_enabled') else 'off'}\n"
            f"Active request: {'yes' if self.requests.has_active(uid) else 'no'}"
        )

    async def link(self, message: Message, command: CommandObject) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if self.requests.has_active(uid):
            await message.answer("Cancel the active request first with /cancel.")
            return
        token = (command.args or "").strip()
        if not token.startswith("cal_"):
            await message.answer("Usage: /link cal_…")
            return
        # The command contains a bearer token. Remove it from Telegram as soon as
        # we have copied it to memory, even if validation later fails.
        try:
            await message.delete()
        except Exception:
            pass
        try:
            tools = await self.calendar.list_tools(token)
            if not tools:
                raise RuntimeError("token exposes no MCP tools")
        except Exception as exc:
            await message.answer(f"Token rejected by calendar MCP: {exc}")
            return
        encrypted = self.crypto.encrypt(token)
        await self.store.update_user(uid, lambda u: u.update({"calendar_token": encrypted, "change_cursor": None}))
        await message.answer(f"Calendar linked. MCP exposes {len(tools)} tool(s).")

    async def unlink(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if self.requests.has_active(uid):
            await message.answer("Cancel the active request first with /cancel.")
            return
        await self.store.update_user(uid, lambda u: u.update({"calendar_token": None, "change_cursor": None}))
        await message.answer("Calendar unlinked.")

    async def codex_login(self, message: Message, bot: Bot) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if self.requests.has_active(uid):
            await message.answer("Cancel the active request first with /cancel.")
            return
        current = self.login_tasks.get(uid)
        if current and not current.done():
            await message.answer("Codex login is already pending.")
            return
        try:
            client, handle = await self.codex.start_device_login(uid)
        except Exception as exc:
            await message.answer(f"Could not start Codex device login: {exc}")
            return
        self.login_handles[uid] = handle
        await message.answer(
            f"Open:\n{handle.verification_url}\n\nCode: {handle.user_code}\n\n"
            "Complete the login. I will save it to your private Codex volume."
        )

        async def waiter() -> None:
            try:
                await asyncio.wait_for(handle.wait(), timeout=self.settings.device_login_timeout_seconds)
                await bot.send_message(uid, "Codex login complete.")
            except asyncio.TimeoutError:
                try:
                    await handle.cancel()
                except Exception:
                    pass
                await bot.send_message(uid, "Codex login expired. Run /codex_login again.")
            except Exception as exc:
                await bot.send_message(uid, f"Codex login failed: {exc}")
            finally:
                self.login_handles.pop(uid, None)
                await client.close()

        self.login_tasks[uid] = asyncio.create_task(waiter())

    async def codex_status(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if not self.codex.auth_path(uid).exists():
            await message.answer("Codex is not logged in.")
            return
        if self.requests.has_active(uid):
            # Avoid opening a second Codex client against the same CODEX_HOME while
            # its ephemeral thread is active. Presence of auth.json is enough here.
            await message.answer("Codex auth file is present. Live account check is skipped while a request is active.")
            return
        try:
            account = await self.codex.account(uid)
            value = getattr(account, "account", None)
            await message.answer("Codex authenticated." + (f"\n{value}" if value else ""))
        except Exception as exc:
            await message.answer(f"Codex auth exists but account check failed: {exc}")

    async def _abandon_login(self, uid: int) -> bool:
        """Drop a pending device login so a new one can be started.

        Without this a login that is never completed holds the slot for the
        whole DEVICE_LOGIN_TIMEOUT_SECONDS, and /codex_login keeps answering
        "already pending" with no way out short of restarting the container.
        """
        handle = self.login_handles.pop(uid, None)
        task = self.login_tasks.pop(uid, None)
        if handle is not None:
            try:
                await handle.cancel()
            except Exception:
                pass
        if task is None or task.done():
            return False
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass
        except Exception:
            pass
        return True

    async def codex_logout(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if self.requests.has_active(uid):
            await message.answer("Cancel the active request first with /cancel.")
            return
        abandoned = await self._abandon_login(uid)
        try:
            await self.codex.logout(uid)
        except Exception:
            self.codex.auth_path(uid).unlink(missing_ok=True)
        await message.answer(
            "Codex logged out."
            + (" Pending device login cancelled; run /codex_login for a new code."
               if abandoned else "")
        )

    async def codex_session(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if self.requests.has_active(uid):
            await message.answer("Cancel the active request first with /cancel.")
            return
        self.pending_auth_upload.add(uid)
        await message.answer("Send your Codex auth.json as the next document. It will be stored with mode 0600 and the Telegram message will be deleted best-effort.")

    async def timezone(self, message: Message, command: CommandObject) -> None:
        if not await self._private_allowed(message):
            return
        value = (command.args or "").strip()
        if not value:
            await message.answer("Usage: /timezone Europe/Vilnius")
            return
        try:
            ZoneInfo(value)
        except Exception:
            await message.answer("Unknown IANA timezone.")
            return
        await self.store.update_user(message.from_user.id, lambda u: u.__setitem__("timezone", value))
        await message.answer(f"Timezone set to {value}.")

    async def poll(self, message: Message, command: CommandObject) -> None:
        if not await self._private_allowed(message):
            return
        value = (command.args or "").strip().lower()
        if value not in {"on", "off"}:
            await message.answer("Usage: /poll on|off")
            return
        enabled = value == "on"
        await self.store.update_user(message.from_user.id, lambda u: u.__setitem__("poll_enabled", enabled))
        await message.answer(f"Polling {value}.")

    async def cancel(self, message: Message) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        auth_pending = uid in self.pending_auth_upload
        self.pending_auth_upload.discard(uid)
        cancelled = await self.requests.cancel(uid)
        if cancelled or auth_pending:
            await message.answer("Cancelled.")
        else:
            await message.answer("No active request. Queue cleared if it existed.")

    async def memory(self, message: Message, command: CommandObject) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        args = (command.args or "").strip()
        if args.startswith("add "):
            text = args[4:].strip()
            if not text:
                await message.answer("Usage: /memory add <text>")
                return
            await self.store.add_memory(uid, text)
            await message.answer("Memory added.")
            return
        if args == "list":
            memories = self.store.list_memories(uid)
            await message.answer("\n".join(f"{i+1}. {x}" for i, x in enumerate(memories)) or "No memories.")
            return
        if args == "clear":
            await self.store.update_user(uid, lambda u: u.__setitem__("memories", []))
            await message.answer("Memories cleared.")
            return
        await message.answer("Usage: /memory add <text> | /memory list | /memory clear")

    async def users(self, message: Message) -> None:
        if not await self._admin(message):
            return
        rows = []
        for uid, user in self.store.list_users().items():
            rows.append(f"{uid}: {user.get('role')} {'enabled' if user.get('enabled') else 'disabled'}")
        await message.answer("\n".join(rows) or "No users.")

    async def user_add(self, message: Message, command: CommandObject) -> None:
        if not await self._admin(message):
            return
        parts = (command.args or "").split()
        if not parts or not parts[0].isdigit() or (len(parts) > 1 and parts[1] not in {"user", "admin"}):
            await message.answer("Usage: /user_add <telegram_id> [user|admin]")
            return
        uid = int(parts[0]); role = parts[1] if len(parts) > 1 else "user"
        await self.store.ensure_user(uid, role)
        await self.store.update_user(uid, lambda u: u.update({"role": role, "enabled": True}))
        await message.answer(f"Added {uid} as {role}.")

    async def user_role(self, message: Message, command: CommandObject) -> None:
        if not await self._admin(message):
            return
        parts = (command.args or "").split()
        if len(parts) != 2 or not parts[0].isdigit() or parts[1] not in {"user", "admin"}:
            await message.answer("Usage: /user_role <telegram_id> <user|admin>")
            return
        uid = int(parts[0])
        if not self.store.get_user(uid):
            await message.answer("Unknown user.")
            return
        if uid in self.settings.admins and parts[1] != "admin":
            await message.answer("Bootstrap admins from ADMIN_IDS cannot be demoted here.")
            return
        await self.store.update_user(uid, lambda u: u.__setitem__("role", parts[1]))
        await message.answer("Role updated.")

    async def user_del(self, message: Message, command: CommandObject) -> None:
        if not await self._admin(message):
            return
        arg = (command.args or "").strip()
        if not arg.isdigit():
            await message.answer("Usage: /user_del <telegram_id>")
            return
        uid = int(arg)
        if uid in self.settings.admins:
            await message.answer("Bootstrap admins from ADMIN_IDS cannot be removed here.")
            return
        await self.requests.cancel(uid)
        login_task = self.login_tasks.pop(uid, None)
        if login_task and not login_task.done():
            login_task.cancel()
            await asyncio.gather(login_task, return_exceptions=True)
        self.pending_auth_upload.discard(uid)
        await self.store.remove_user(uid)
        shutil.rmtree(self.settings.codex_dir / str(uid), ignore_errors=True)
        await message.answer(f"Removed {uid}.")

    async def catch_all(self, message: Message, bot: Bot) -> None:
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        if uid in self.pending_auth_upload:
            if not message.document:
                await message.answer("Send auth.json as a Telegram document, or run /codex_session again later.")
                return
            await self._import_auth_document(message, bot)
            return
        if (message.text or "").startswith("/"):
            await message.answer("Unknown command. Use /help.")
            return
        if message.voice:
            await message.answer("Voice messages are disabled.")
            return
        if message.media_group_id:
            await self._collect_album(message, bot)
            return
        await self._submit_messages([message], bot)

    async def edited(self, message: Message, bot: Bot) -> None:
        """Re-run a request when its message is edited.

        Editing is how people correct a request they got wrong. If the original
        is still running the edit simply replaces it. If it already finished,
        the edit is run as a correction rather than a fresh instruction, and
        nothing the first run applied is undone: this handler never guesses at
        reversing a mutation, so a correction can add or change but the user
        stays responsible for removing anything the first attempt created.
        """
        if not await self._private_allowed(message):
            return
        uid = message.from_user.id
        text = (message.text or "").strip()
        if not text or text.startswith("/") or uid in self.pending_auth_upload:
            return

        if self.requests.waiting_for_answer(uid):
            # Editing the answer to a clarification is just a better answer.
            await self._submit_messages([message], bot)
            return

        if self.requests.has_active(uid):
            await self.requests.cancel(uid)
            await message.reply("Edited — restarting with the new text.")
            await self._submit_messages([message], bot)
            return

        await message.reply("Edited — running the corrected request.")
        await self._submit_messages([message], bot, text_override=(
            "This is a correction of an earlier message you already acted on. "
            "Treat the following as the intended request. Check the current state "
            "before acting so you do not duplicate what was already done, and do "
            "not undo anything unless it is explicitly asked for.\n\n" + text
        ))

    async def _import_auth_document(self, message: Message, bot: Bot) -> None:
        uid = message.from_user.id
        staging = self.settings.work_dir / ".auth" / str(uid)
        staging.mkdir(parents=True, exist_ok=True)
        path = staging / "auth-upload.json"
        try:
            await bot.download(message.document, destination=path)
            # The Telegram message itself contains account credentials. Remove it as
            # soon as the bytes are local, even if validation fails afterward.
            try:
                await message.delete()
            except Exception:
                pass
            self.codex.import_auth_json(uid, path)
            # No request can be active here. Reset any stale request-scoped MCP config
            # left by a previous crash before starting Codex to validate the account.
            self.codex.write_config(uid)
            await self.codex.account(uid)
        except Exception as exc:
            self.codex.auth_path(uid).unlink(missing_ok=True)
            await bot.send_message(uid, f"auth.json rejected: {exc}")
            return
        finally:
            self.pending_auth_upload.discard(uid)
            shutil.rmtree(staging, ignore_errors=True)
        await bot.send_message(uid, "Codex session imported and verified.")

    async def _collect_album(self, message: Message, bot: Bot) -> None:
        key = f"{message.chat.id}:{message.media_group_id}"
        self.albums.setdefault(key, []).append(message)
        old = self.album_tasks.get(key)
        if old and not old.done():
            old.cancel()

        async def flush() -> None:
            try:
                await asyncio.sleep(0.8)
                messages = self.albums.pop(key, [])
                self.album_tasks.pop(key, None)
                if messages:
                    await self._submit_messages(messages, bot)
            except asyncio.CancelledError:
                pass

        self.album_tasks[key] = asyncio.create_task(flush())

    async def _submit_messages(self, messages: list[Message], bot: Bot,
                               text_override: str | None = None) -> None:
        first = messages[0]
        uid = first.from_user.id
        stage = self.settings.work_dir / ".staging" / str(uid) / uuid.uuid4().hex
        stage.mkdir(parents=True, exist_ok=True)
        paths: list[Path] = []
        images: list[Path] = []
        text = ""
        try:
            for i, msg in enumerate(messages):
                text = text or (msg.text or msg.caption or "")
                obj: Any | None = None
                filename: str | None = None
                image = False
                if msg.document:
                    obj = msg.document
                    filename = msg.document.file_name
                elif msg.photo:
                    obj = msg.photo[-1]
                    filename = f"photo_{i+1}.jpg"
                    image = True
                elif msg.video:
                    obj = msg.video
                    filename = msg.video.file_name or f"video_{i+1}.mp4"
                elif msg.audio:
                    obj = msg.audio
                    filename = msg.audio.file_name or f"audio_{i+1}.bin"
                if obj is None:
                    continue
                size = getattr(obj, "file_size", None)
                if size and size > self.settings.max_file_bytes:
                    await first.answer(f"File too large for this bot configuration: {filename or 'file'}")
                    shutil.rmtree(stage, ignore_errors=True)
                    return
                path = stage / f"{i+1:02d}_{safe_filename(filename)}"
                await bot.download(obj, destination=path)
                paths.append(path)
                if image:
                    images.append(path)
            await self.requests.submit(uid, Incoming(chat_id=first.chat.id, text=text_override or text,
                                                    files=paths, image_paths=images))
            # Text-only messages do not need a staging directory; file-backed
            # requests keep theirs until RequestManager moves/cleans the files.
            if not paths:
                shutil.rmtree(stage, ignore_errors=True)
        except Exception:
            shutil.rmtree(stage, ignore_errors=True)
            raise
