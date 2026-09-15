from cryptography.fernet import Fernet

from app.config import Settings


def base_env(tmp_path):
    return {
        "telegram_bot_token": "x",
        "admin_ids": "1",
        "state_encryption_key": Fernet.generate_key().decode(),
        "data_dir": tmp_path,
    }


def test_data_dir_derives_all_default_paths(tmp_path):
    s = Settings(**base_env(tmp_path))
    assert s.state_file == tmp_path / "state.json"
    assert s.work_dir == tmp_path / "work"
    assert s.codex_dir == tmp_path / "codex"


def test_capability_ttl_must_outlive_turn_and_question(tmp_path):
    args = base_env(tmp_path)
    args.update({"request_timeout_seconds": 100, "ask_timeout_seconds": 200, "capability_ttl_seconds": 150})
    try:
        Settings(**args)
    except Exception as exc:
        assert "CAPABILITY_TTL_SECONDS" in str(exc)
    else:
        raise AssertionError("expected validation failure")
