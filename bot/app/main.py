from __future__ import annotations

import asyncio
import logging
import shutil

from aiogram import Bot, Dispatcher
from aiogram.client.session.aiohttp import AiohttpSession
from aiogram.exceptions import TelegramBadRequest
from aiogram.types import LinkPreviewOptions

from .calendar_mcp import CalendarMCP
from .codex_runtime import CodexRuntime
from .config import Settings
from .crypto import SecretBox
from .guard import GuardServer
from .handlers import Handlers
from .poller import CalendarPoller
from .requests import RequestManager
from .store import Store
from .utils import split_message, telegram_html

log = logging.getLogger(__name__)


async def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    settings = Settings()
    settings.ensure_dirs()

    assert settings.state_file is not None and settings.work_dir is not None and settings.codex_dir is not None
    crypto = SecretBox(settings.state_encryption_key)
    store = Store(settings.state_file, settings.max_history_items, crypto)
    await store.load()
    await store.bootstrap_admins(settings.admins)
    calendar = CalendarMCP(
        settings.calendar_mcp_url,
        settings.calendar_proxy,
        settings.tool_cache_ttl_seconds,
    )
    codex = CodexRuntime(settings)
    # Ephemeral requests cannot survive process restart. Remove stale request MCP
    # capabilities from each existing CODEX_HOME before accepting commands.
    for user_id in store.list_user_ids():
        try:
            codex.write_config(user_id)
        except Exception:
            logging.getLogger(__name__).warning("could not reset Codex config for %s", user_id, exc_info=True)

    # One-shot MCP capabilities never need to survive a process restart.
    for cap_file in settings.cap_dir.glob("*.cap"):
        try:
            cap_file.unlink()
        except OSError:
            logging.getLogger(__name__).warning("could not remove stale capability file %s", cap_file, exc_info=True)

    telegram_session = AiohttpSession(proxy=settings.telegram_proxy) if settings.telegram_proxy else AiohttpSession()
    bot = Bot(settings.telegram_bot_token, session=telegram_session)

    async def send(chat_id: int, text: str) -> None:
        # The model answers in Markdown. Telegram renders a small HTML subset;
        # anything that still fails to parse is sent as plain text rather than
        # lost, because a dropped reply is worse than an unformatted one.
        for chunk in split_message(text):
            try:
                await bot.send_message(chat_id, telegram_html(chunk), parse_mode="HTML",
                                       link_preview_options=LinkPreviewOptions(is_disabled=True))
            except TelegramBadRequest:
                log.warning("falling back to plain text for chat %s", chat_id, exc_info=True)
                await bot.send_message(chat_id, chunk)

    guard = GuardServer(calendar, store, settings.guard_host, settings.guard_port, settings.capability_ttl_seconds)
    await guard.start()
    requests = RequestManager(
        store, crypto, guard, codex, settings.request_timeout_seconds,
        settings.ask_timeout_seconds, send, settings.work_dir, settings.default_timezone,
    )
    handlers = Handlers(settings, store, crypto, calendar, codex, requests)
    poller = CalendarPoller(
        store, crypto, calendar, send, settings.poll_interval_seconds,
        settings.agenda_hour, settings.default_timezone,
    )
    dp = Dispatcher()
    dp.include_router(handlers.router)

    # Ephemeral Codex threads cannot survive restart. Remove only transient workdirs.
    shutil.rmtree(settings.work_dir / ".staging", ignore_errors=True)
    shutil.rmtree(settings.work_dir / ".auth", ignore_errors=True)
    for p in settings.work_dir.iterdir():
        if p.name.startswith("."):
            continue
        if p.is_dir():
            shutil.rmtree(p, ignore_errors=True)

    poller.start()
    try:
        await dp.start_polling(bot, allowed_updates=dp.resolve_used_update_types())
    finally:
        await poller.stop()
        await requests.shutdown()
        await handlers.shutdown()
        await guard.stop()
        await bot.session.close()


if __name__ == "__main__":
    asyncio.run(main())
