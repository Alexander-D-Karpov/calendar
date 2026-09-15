import asyncio

from app.mcp_bridge import GuardProxy, read_one_shot_capability


def test_guard_proxy_rejects_use_before_context():
    async def run():
        proxy = GuardProxy("http://127.0.0.1:1", "cap")
        try:
            await proxy.post("tools", {})
        except RuntimeError as exc:
            assert "not started" in str(exc)
        else:
            raise AssertionError("expected RuntimeError")

    asyncio.run(run())


def test_guard_proxy_normalizes_tools_and_calls(monkeypatch):
    async def run():
        proxy = GuardProxy("http://guard", "cap")

        async def fake_post(path, body):
            if path == "tools":
                return 200, {"tools": [{"name": "x"}, "bad"]}
            assert body == {"name": "x", "arguments": {"a": 1}}
            return 200, {"result": {"ok": True}}

        monkeypatch.setattr(proxy, "post", fake_post)
        assert await proxy.tools() == [{"name": "x"}]
        assert await proxy.call("x", {"a": 1}) == (True, {"ok": True})

    asyncio.run(run())


def test_capability_file_is_one_shot(tmp_path):
    path = tmp_path / "cap"
    cap = "x" * 48
    path.write_text(cap, encoding="utf-8")
    assert read_one_shot_capability(path) == cap
    assert not path.exists()
