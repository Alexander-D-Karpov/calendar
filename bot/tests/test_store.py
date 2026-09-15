import asyncio
import json

from cryptography.fernet import Fernet

from app.crypto import SecretBox
from app.store import Store


def test_store_roundtrip(tmp_path):
    async def run():
        store = Store(tmp_path / "state.json", 2)
        await store.load()
        await store.ensure_user(10)
        await store.add_history(10, "a", "b")
        await store.add_history(10, "c", "d")
        await store.add_history(10, "e", "f")
        raw = json.loads((tmp_path / "state.json").read_text())
        assert len(raw["users"]["10"]["history"]) == 2
        store2 = Store(tmp_path / "state.json", 2)
        await store2.load()
        assert store2.is_allowed(10)
    asyncio.run(run())


def test_sensitive_context_is_encrypted_at_rest(tmp_path):
    async def run():
        box = SecretBox(Fernet.generate_key().decode())
        path = tmp_path / "state.json"
        store = Store(path, 5, box)
        await store.load()
        await store.ensure_user(10)
        await store.add_history(10, "private meeting with Alice", "created event 123")
        await store.add_memory(10, "Alice means the Work calendar")

        raw_text = path.read_text(encoding="utf-8")
        assert "private meeting" not in raw_text
        assert "Alice means" not in raw_text
        assert "created event 123" not in raw_text

        matches = store.search_context(10, "Alice", 5)
        assert any("private meeting" in item.get("request", "") for item in matches)
        assert "Alice means the Work calendar" in store.list_memories(10)
    asyncio.run(run())


def test_plaintext_v1_context_migrates_to_encrypted_v2(tmp_path):
    async def run():
        path = tmp_path / "state.json"
        path.write_text(json.dumps({
            "version": 1,
            "users": {
                "10": {
                    "enabled": True,
                    "role": "user",
                    "history": [{"at": "x", "request": "old secret", "result": "old result"}],
                    "memories": ["old memory"],
                }
            },
        }), encoding="utf-8")
        box = SecretBox(Fernet.generate_key().decode())
        store = Store(path, 5, box)
        await store.load()
        raw = path.read_text(encoding="utf-8")
        assert "old secret" not in raw
        assert "old result" not in raw
        assert "old memory" not in raw
        assert store.search_context(10, "secret", 5)[0]["request"] == "old secret"
        assert store.list_memories(10) == ["old memory"]
    asyncio.run(run())


def test_bootstrap_admins_are_created_promoted_enabled_and_persisted(tmp_path):
    async def run():
        path = tmp_path / "state.json"
        store = Store(path, 5)
        await store.load()
        await store.ensure_user(10, "user")
        await store.update_user(10, lambda u: u.update({"enabled": False, "role": "user"}))

        await store.bootstrap_admins({10, 20})

        assert store.is_admin(10)
        assert store.is_admin(20)
        reloaded = Store(path, 5)
        await reloaded.load()
        assert reloaded.is_admin(10)
        assert reloaded.is_admin(20)

    asyncio.run(run())


def test_bootstrap_admins_rejects_empty_set(tmp_path):
    async def run():
        store = Store(tmp_path / "state.json", 5)
        await store.load()
        try:
            await store.bootstrap_admins(set())
        except RuntimeError as exc:
            assert "ADMIN_IDS" in str(exc)
        else:
            raise AssertionError("expected RuntimeError")

    asyncio.run(run())


def test_version_only_v1_state_is_rewritten_to_v2(tmp_path):
    async def run():
        path = tmp_path / "state.json"
        path.write_text(json.dumps({"version": 1, "users": {}}), encoding="utf-8")
        store = Store(path, 5)
        await store.load()
        raw = json.loads(path.read_text(encoding="utf-8"))
        assert raw["version"] == 2

    asyncio.run(run())


def test_list_user_ids_does_not_require_copying_records(tmp_path):
    store = Store(tmp_path / "state.json", 5)
    store.data["users"] = {"10": {"large": [1] * 100}, "20": {"large": [2] * 100}}
    assert store.list_user_ids() == [10, 20]
