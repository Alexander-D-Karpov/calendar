import sys
import tomllib
import types
from pathlib import Path


# The packaging harness may not have openai-codex installed. Stub only the
# import surface needed to exercise config generation; Docker's runtime smoke
# uses the real pinned package and app-server.
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

from app.codex_runtime import CodexRuntime


class DummySettings:
    def __init__(self, base: Path):
        self.codex_dir = base / "codex"
        self.cap_dir = base / "caps"
        self.no_proxy = "127.0.0.1,localhost"
        self.codex_http_proxy = None
        self.codex_https_proxy = None
        self.codex_all_proxy = None
        self.codex_reasoning_effort = "medium"
        self.codex_model = None


def test_request_config_is_valid_toml_and_cap_not_embedded(tmp_path):
    settings = DummySettings(tmp_path)
    runtime = CodexRuntime(settings)
    workdir = tmp_path / "work" / "r1"
    workdir.mkdir(parents=True)
    secret = "capability-not-a-calendar-token-but-still-secret-123456"
    cap_file = runtime._write_cap_file(42, secret)

    path = runtime.write_config(
        42,
        guard_url="http://127.0.0.1:8765",
        cap_file=cap_file,
        workdir=workdir,
    )
    raw = path.read_text(encoding="utf-8")
    data = tomllib.loads(raw)

    assert data["history"]["persistence"] == "none"
    assert data["web_search"] == "disabled"
    assert data["allow_login_shell"] is False
    assert data["shell_environment_policy"]["inherit"] == "core"
    assert data["shell_environment_policy"]["ignore_default_excludes"] is False
    assert data["default_permissions"] == "calendar_bot"
    fs = data["permissions"]["calendar_bot"]["filesystem"]
    assert fs[":root"] == "deny"
    assert fs[":minimal"] == "read"
    assert fs[str(workdir.resolve())] == "read"
    assert data["permissions"]["calendar_bot"]["network"]["enabled"] is False
    args = data["mcp_servers"]["calendar"]["args"]
    assert "--cap-file" in args
    assert str(cap_file.resolve()) in args
    assert secret not in raw
    assert "cal_" not in raw
    assert cap_file.read_text(encoding="utf-8") == secret


def test_codex_environment_does_not_inherit_bot_secrets(tmp_path, monkeypatch):
    settings = DummySettings(tmp_path)
    settings.codex_http_proxy = "http://proxy.example:8080"
    runtime = CodexRuntime(settings)
    monkeypatch.setenv("TELEGRAM_BOT_TOKEN", "telegram-secret")
    monkeypatch.setenv("STATE_ENCRYPTION_KEY", "fernet-secret")
    monkeypatch.setenv("UNRELATED_PRIVATE_SECRET", "private")
    monkeypatch.setenv("PATH", "/usr/bin")

    env = runtime._env(42)

    assert env["PATH"] == "/usr/bin"
    assert env["CODEX_HOME"].endswith("/42")
    assert env["HTTP_PROXY"] == "http://proxy.example:8080"
    assert "TELEGRAM_BOT_TOKEN" not in env
    assert "STATE_ENCRYPTION_KEY" not in env
    assert "UNRELATED_PRIVATE_SECRET" not in env
