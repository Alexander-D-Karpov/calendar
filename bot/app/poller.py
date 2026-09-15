from __future__ import annotations

import asyncio
import logging
from datetime import datetime, timedelta
from typing import Any, Awaitable, Callable
from zoneinfo import ZoneInfo

from .calendar_mcp import CalendarMCP
from .crypto import SecretBox
from .store import Store
from .utils import local_time_label

SendFn = Callable[[int, str], Awaitable[None]]
log = logging.getLogger(__name__)


class CalendarPoller:
    def __init__(
        self,
        store: Store,
        crypto: SecretBox,
        calendar: CalendarMCP,
        send: SendFn,
        interval_seconds: int,
        agenda_hour: int,
        default_timezone: str,
    ) -> None:
        self.store = store
        self.crypto = crypto
        self.calendar = calendar
        self.send = send
        self.interval = max(10, interval_seconds)
        self.agenda_hour = agenda_hour
        self.default_timezone = default_timezone
        self._task: asyncio.Task[None] | None = None
        self._stopping = asyncio.Event()
        self._missing_logged: set[tuple[int, str]] = set()

    def start(self) -> None:
        if not self._task or self._task.done():
            self._task = asyncio.create_task(self._run())

    async def stop(self) -> None:
        self._stopping.set()
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

    async def _run(self) -> None:
        while not self._stopping.is_set():
            for user_id in self.store.list_user_ids():
                user = self.store.get_user(user_id) or {}
                if not user.get("enabled") or not user.get("poll_enabled") or not user.get("calendar_token"):
                    continue
                try:
                    await self._poll_user(user_id, user)
                except Exception:
                    log.warning("calendar poll failed for Telegram user %s", user_id, exc_info=True)
            try:
                await asyncio.wait_for(self._stopping.wait(), timeout=self.interval)
            except asyncio.TimeoutError:
                pass

    async def _resolve(self, user_id: int, token: str, logical: str) -> str | None:
        actual = await self.calendar.resolve_tool_name(token, logical)
        key = (user_id, logical)
        if actual is None:
            if key not in self._missing_logged:
                log.warning("calendar MCP token for user %s does not expose an unambiguous %s tool", user_id, logical)
                self._missing_logged.add(key)
            return None
        self._missing_logged.discard(key)
        return actual

    async def _poll_user(self, user_id: int, user: dict[str, Any]) -> None:
        token = self.crypto.decrypt(user["calendar_token"])
        list_changes = await self._resolve(user_id, token, "listChanges")
        if list_changes:
            cursor = user.get("change_cursor")
            if cursor is None:
                data = await self.calendar.call(token, list_changes, {"since": 0, "limit": 1})
                if isinstance(data, dict) and isinstance(data.get("latest"), int):
                    latest = data["latest"]
                    await self.store.update_user(user_id, lambda u: u.__setitem__("change_cursor", latest))
            else:
                data = await self.calendar.call(token, list_changes, {"since": int(cursor), "limit": 200})
                if isinstance(data, dict):
                    latest = data.get("latest")
                    if isinstance(latest, int) and latest != cursor:
                        await self.store.update_user(user_id, lambda u: u.__setitem__("change_cursor", latest))
                    # Avoid echoing ordinary writes from this app; surface external sync origins.
                    external = [x for x in (data.get("items") or []) if isinstance(x, dict) and x.get("origin")]
                    if external:
                        lines = []
                        for item in external[:8]:
                            lines.append(
                                f"{item.get('entity','item')} {item.get('op','changed')}: "
                                f"{item.get('id','?')} ({item.get('origin')})"
                            )
                        if len(external) > 8:
                            lines.append(f"…and {len(external) - 8} more")
                        await self.send(user_id, "External calendar changes:\n" + "\n".join(lines))

        await self._maybe_send_agenda(user_id, user, token)

    async def _maybe_send_agenda(self, user_id: int, user: dict[str, Any], token: str) -> None:
        tz_name = user.get("timezone") or self.default_timezone
        try:
            now = datetime.now(ZoneInfo(tz_name))
        except Exception:
            now = datetime.now(ZoneInfo(self.default_timezone))
        if now.hour < self.agenda_hour or user.get("last_agenda_date") == now.date().isoformat():
            return

        list_events = await self._resolve(user_id, token, "listEvents")
        list_todos = await self._resolve(user_id, token, "listTodos")
        if not list_events or not list_todos:
            return

        today = now.date()
        tomorrow = today + timedelta(days=1)
        try:
            events = await self.calendar.call(
                token,
                list_events,
                {"from": today.isoformat(), "to": tomorrow.isoformat()},
            )
            todos = await self.calendar.call(
                token,
                list_todos,
                {
                    "due_from": today.isoformat(),
                    "due_to": tomorrow.isoformat(),
                    "status": "open",
                    "limit": 100,
                },
            )
        except Exception:
            log.warning("agenda fetch failed for Telegram user %s", user_id, exc_info=True)
            return

        event_items = events.get("items", []) if isinstance(events, dict) else []
        todo_items = todos.get("items", []) if isinstance(todos, dict) else []
        lines = [f"Agenda for {today.isoformat()}:"]
        tz = now.tzinfo
        if event_items:
            lines.append("Events:")
            for e in event_items[:12]:
                if not isinstance(e, dict):
                    continue
                when = local_time_label(e.get("start"), tz)
                title = e.get("title") or "(untitled)"
                lines.append(f"• {when} — {title}" if when else f"• {title}")
        if todo_items:
            lines.append("Todos:")
            for t in todo_items[:12]:
                if not isinstance(t, dict):
                    continue
                # The agenda only covers today, so a todo with no due time adds
                # nothing by repeating today's date.
                due = local_time_label(t.get("due_time") or t.get("due_date"), tz)
                title = t.get("title") or "(untitled)"
                lines.append(f"• {due} — {title}" if due and due != "all day" else f"• {title}")
        if not event_items and not todo_items:
            lines.append("Nothing scheduled or due today.")
        await self.send(user_id, "\n".join(lines))
        await self.store.update_user(user_id, lambda u: u.__setitem__("last_agenda_date", today.isoformat()))
