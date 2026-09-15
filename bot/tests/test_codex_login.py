import asyncio

import pytest

# handlers.py pulls in the full runtime dependency set at module scope. Stubbing
# all of it would leave a test made mostly of scaffolding, so skip where those
# are absent; the image the bot actually ships in has them and runs this.
for _module in ("aiogram", "httpx2", "mcp", "openai_codex"):
    pytest.importorskip(_module)

from app.handlers import Handlers  # noqa: E402


def _bare_handlers() -> Handlers:
    """_abandon_login only touches the two login dicts, so skip __init__ and
    its full dependency graph."""
    h = Handlers.__new__(Handlers)
    h.login_tasks = {}
    h.login_handles = {}
    return h


class FakeHandle:
    def __init__(self) -> None:
        self.cancelled = False

    async def cancel(self) -> None:
        self.cancelled = True


def test_abandon_login_releases_a_pending_login():
    async def run():
        h = _bare_handlers()
        handle = FakeHandle()
        started = asyncio.Event()

        async def waiter():
            started.set()
            await asyncio.sleep(3600)  # the real one waits for the device flow

        h.login_handles[7] = handle
        h.login_tasks[7] = asyncio.create_task(waiter())
        await started.wait()

        assert await h._abandon_login(7) is True
        assert handle.cancelled, "device handle must be cancelled, not just forgotten"
        assert 7 not in h.login_tasks
        assert 7 not in h.login_handles
        # The slot is free, so /codex_login would no longer answer "already pending".
        current = h.login_tasks.get(7)
        assert current is None or current.done()

    asyncio.run(run())


def test_abandon_login_is_a_noop_without_a_pending_login():
    async def run():
        h = _bare_handlers()
        assert await h._abandon_login(7) is False

    asyncio.run(run())


def test_abandon_login_survives_a_handle_that_fails_to_cancel():
    async def run():
        class Angry:
            async def cancel(self):
                raise RuntimeError("device endpoint is unreachable")

        h = _bare_handlers()
        started = asyncio.Event()

        async def waiter():
            started.set()
            await asyncio.sleep(3600)

        h.login_handles[7] = Angry()
        h.login_tasks[7] = asyncio.create_task(waiter())
        await started.wait()

        # A failure to reach the device endpoint must not strand the local slot.
        assert await h._abandon_login(7) is True
        assert 7 not in h.login_tasks

    asyncio.run(run())
