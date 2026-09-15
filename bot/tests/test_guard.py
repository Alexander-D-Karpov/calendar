import asyncio
import time

from app.guard import Capability, GuardServer
from app.store import Store


class FakeCalendar:
    def __init__(self):
        self.calls = []

    async def list_tools(self, token):
        return []

    async def call(self, token, name, arguments=None):
        self.calls.append((name, arguments or {}))
        if name == "getEvent":
            return {"id": arguments["id"], "recurring": True}
        if name == "listTodos":
            return {"items": []}
        return {"ok": True}


def make_cap():
    return Capability(
        user_id=1,
        token="cal_super_secret_token_123456789",
        tools=[
            {"name": "getEvent", "inputSchema": {}},
            {"name": "updateEvent", "inputSchema": {}},
            {"name": "deleteEvent", "inputSchema": {}},
            {"name": "deleteTodo", "inputSchema": {}},
            {"name": "deleteCheck", "inputSchema": {}},
            {"name": "listTodos", "inputSchema": {}},
            {"name": "deleteCalendar", "inputSchema": {}},
            {"name": "resolveDuplicate", "inputSchema": {}},
            {"name": "createShare", "inputSchema": {}},
            {"name": "revokeShare", "inputSchema": {}},
            {"name": "commitImport", "inputSchema": {}},
            {"name": "updateMe", "inputSchema": {}},
            {"name": "createSubscription", "inputSchema": {}},
            {"name": "refreshSubscription", "inputSchema": {}},
        ],
        expires_at=time.monotonic() + 100,
    )


def test_dangerous_tools_hidden_and_privacy_revoke_allowed(tmp_path):
    s = Store(tmp_path / "s.json")
    g = GuardServer(FakeCalendar(), s, "127.0.0.1", 9999, 10)
    for name in [
        "deleteCalendar", "resolveDuplicate", "createShare", "commitImport",
        "updateMe", "createSubscription", "refreshSubscription",
    ]:
        assert not g._safe_tool({"name": name})
    assert g._safe_tool({"name": "revokeShare"})
    assert g._safe_tool({"name": "deleteEvent"})
    assert g._safe_tool({"name": "deleteCheck"})
    assert g._safe_tool({"name": "updateEvent"})


def test_capability_touch_extends_deadline(tmp_path):
    s = Store(tmp_path / "s.json")
    g = GuardServer(FakeCalendar(), s, "127.0.0.1", 9999, 100)
    cap = make_cap()
    cap.expires_at = time.monotonic() + 1
    g._caps["abc"] = cap
    before = cap.expires_at
    assert g.touch("abc") is True
    assert cap.expires_at > before
    assert g.touch("missing") is False


def test_recurring_delete_requires_instance(tmp_path):
    async def run():
        s = Store(tmp_path / "s.json")
        await s.load()
        c = FakeCalendar()
        g = GuardServer(c, s, "127.0.0.1", 9999, 10)
        cap = make_cap()
        try:
            await g._preflight_event_delete(cap, {"id": "e1"})
        except ValueError as exc:
            assert "recurring" in str(exc)
        else:
            raise AssertionError("expected rejection")
        await g._preflight_event_delete(
            cap,
            {"id": "e1", "scope": "this", "instance": "2026-09-15T10:00:00+03:00"},
        )
    asyncio.run(run())


def test_event_updates_are_not_forced_to_single_occurrence(tmp_path):
    # The one-entry safety rule applies to deletion, not ordinary edits. Whole
    # series/following updates remain available to the agent when requested.
    s = Store(tmp_path / "s.json")
    g = GuardServer(FakeCalendar(), s, "127.0.0.1", 9999, 10)
    assert g._safe_tool({"name": "updateEvent"})
    assert not hasattr(g, "_preflight_event_update")


def test_todo_with_child_blocked(tmp_path):
    class ChildCalendar(FakeCalendar):
        async def call(self, token, name, arguments=None):
            if name == "listTodos":
                return {"items": [{"id": "child"}]}
            return await super().call(token, name, arguments)

    async def run():
        s = Store(tmp_path / "s.json")
        await s.load()
        g = GuardServer(ChildCalendar(), s, "127.0.0.1", 9999, 10)
        try:
            await g._preflight_todo_delete(make_cap(), {"id": "todo"})
        except ValueError as exc:
            assert "subtasks" in str(exc)
        else:
            raise AssertionError("expected rejection")
    asyncio.run(run())


def test_capability_touch_does_not_resurrect_expired_cap(tmp_path):
    s = Store(tmp_path / "s.json")
    g = GuardServer(FakeCalendar(), s, "127.0.0.1", 9999, 100)
    cap = make_cap()
    cap.expires_at = time.monotonic() - 1
    g._caps["expired"] = cap
    assert g.touch("expired") is False
    assert "expired" not in g._caps


def test_error_redaction_removes_calendar_and_bearer_tokens(tmp_path):
    s = Store(tmp_path / "s.json")
    g = GuardServer(FakeCalendar(), s, "127.0.0.1", 9999, 100)
    token = "cal_super_secret_token_123456789"
    text = g._safe_error(
        RuntimeError(f"Authorization: Bearer {token}; token={token}"),
        token,
    )
    assert token not in text
    assert "[redacted]" in text
