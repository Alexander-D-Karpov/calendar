import asyncio
import sys
import types
from pathlib import Path

# requests.py imports codex_runtime for type/runtime names. Stub the external SDK
# surface only if the packaging harness does not have openai-codex installed.
try:
    import openai_codex  # noqa: F401
except ImportError:
    mod = types.ModuleType("openai_codex")

    class _ApprovalMode:
        deny_all = "deny-all"

    class _Dummy:
        def __init__(self, *args, **kwargs):
            pass

    mod.ApprovalMode = _ApprovalMode
    mod.AsyncCodex = _Dummy
    mod.CodexConfig = _Dummy
    mod.LocalImageInput = _Dummy
    mod.TextInput = _Dummy
    sys.modules["openai_codex"] = mod

from app.requests import Incoming, RequestManager


class FakeStore:
    def __init__(self):
        self.history = []

    def get_user(self, user_id):
        return {"calendar_token": "enc", "timezone": "Europe/Vilnius"}

    async def add_history(self, user_id, request, result):
        self.history.append((user_id, request, result))


class FakeCrypto:
    def decrypt(self, value):
        assert value == "enc"
        return "cal_token"


class FakeGuard:
    def __init__(self):
        self.base_url = "http://127.0.0.1:1"
        self.touches = 0
        self.revoked = []

    async def create_capability(self, user_id, token):
        return "cap"

    def touch(self, cap):
        assert cap == "cap"
        self.touches += 1
        return True

    def revoke(self, cap):
        self.revoked.append(cap)


class FakeSession:
    def __init__(self, responses):
        self.responses = list(responses)
        self.closed = False

    async def run(self, text, images):
        return self.responses.pop(0)

    async def close(self):
        self.closed = True


class FakeCodex:
    def __init__(self, root: Path, session: FakeSession):
        self.root = root
        self.session = session
        self.write_calls = 0
        p = self.auth_path(1)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text("{}")

    def auth_path(self, user_id):
        return self.root / str(user_id) / "auth.json"

    async def new_session(self, *args, **kwargs):
        return self.session

    def write_config(self, *args, **kwargs):
        self.write_calls += 1
        raise AssertionError("RequestManager must not rewrite shared Codex config during finish")


async def wait_until(predicate, timeout=1.0):
    deadline = asyncio.get_running_loop().time() + timeout
    while not predicate():
        if asyncio.get_running_loop().time() >= deadline:
            raise AssertionError("condition timed out")
        await asyncio.sleep(0.005)


def make_manager(tmp_path, responses, ask_timeout=0.05):
    sent = []

    async def send(chat_id, text):
        sent.append((chat_id, text))

    store = FakeStore()
    guard = FakeGuard()
    session = FakeSession(responses)
    codex = FakeCodex(tmp_path / "codex", session)
    manager = RequestManager(
        store,
        FakeCrypto(),
        guard,
        codex,
        timeout=1,
        ask_timeout=ask_timeout,
        send=send,
        work_root=tmp_path / "work",
        default_timezone="Europe/Vilnius",
    )
    return manager, store, guard, codex, session, sent


def test_unanswered_question_expires_and_releases_queue(tmp_path):
    async def run():
        manager, _, guard, codex, session, sent = make_manager(
            tmp_path,
            [{"status": "ask", "text": "Which calendar?", "refs": []}],
            ask_timeout=0.03,
        )
        await manager.submit(1, Incoming(chat_id=1, text="create meeting"))
        await wait_until(lambda: manager.waiting_for_answer(1), timeout=0.5)
        await wait_until(lambda: not manager.has_active(1), timeout=0.5)
        assert any("No answer received" in text for _, text in sent)
        assert guard.revoked == ["cap"]
        assert session.closed
        assert codex.write_calls == 0
        await manager.shutdown()

    asyncio.run(run())


def test_answer_continues_same_session_and_refreshes_capability(tmp_path):
    async def run():
        manager, store, guard, codex, session, sent = make_manager(
            tmp_path,
            [
                {"status": "ask", "text": "Which calendar?", "refs": []},
                {"status": "done", "text": "Created", "refs": ["e1"]},
            ],
            ask_timeout=0.2,
        )
        await manager.submit(1, Incoming(chat_id=1, text="create meeting"))
        await wait_until(lambda: manager.waiting_for_answer(1))
        touches_before = guard.touches
        await manager.submit(1, Incoming(chat_id=1, text="Work"))
        await wait_until(lambda: not manager.has_active(1))
        assert guard.touches > touches_before
        assert store.history and store.history[0][2] == "Created"
        assert codex.write_calls == 0
        assert session.closed
        await manager.shutdown()

    asyncio.run(run())


def test_worker_cancellation_tears_down_active_request(tmp_path):
    class BlockingSession(FakeSession):
        def __init__(self):
            super().__init__([])
            self.started = asyncio.Event()
            self.block = asyncio.Event()

        async def run(self, text, images):
            self.started.set()
            await self.block.wait()
            return {"status": "done", "text": "unexpected", "refs": []}

    async def run():
        sent = []

        async def send(chat_id, text):
            sent.append((chat_id, text))

        store = FakeStore()
        guard = FakeGuard()
        session = BlockingSession()
        codex = FakeCodex(tmp_path / "codex", session)
        manager = RequestManager(
            store,
            FakeCrypto(),
            guard,
            codex,
            timeout=30,
            ask_timeout=30,
            send=send,
            work_root=tmp_path / "work",
            default_timezone="Europe/Vilnius",
        )

        await manager.submit(1, Incoming(chat_id=1, text="long request"))
        await asyncio.wait_for(session.started.wait(), timeout=0.5)
        await wait_until(lambda: manager.has_active(1), timeout=0.5)

        worker = manager.workers[1]
        worker.cancel()
        await asyncio.gather(worker, return_exceptions=True)

        assert not manager.has_active(1)
        assert guard.revoked == ["cap"]
        assert session.closed
        assert codex.write_calls == 0
        assert not list((tmp_path / "work").glob("1/*"))

    asyncio.run(run())
