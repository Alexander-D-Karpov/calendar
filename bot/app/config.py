from __future__ import annotations

from pathlib import Path

from pydantic import field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    telegram_bot_token: str
    admin_ids: str
    state_encryption_key: str

    calendar_mcp_url: str = "https://calendar.akarpov.ru/api/mcp"
    data_dir: Path = Path("/data")
    state_file: Path | None = None
    work_dir: Path | None = None
    codex_dir: Path | None = None
    cap_dir: Path = Path("/run/calendar-codex-bot/caps")

    telegram_proxy: str | None = None
    calendar_proxy: str | None = None
    codex_http_proxy: str | None = None
    codex_https_proxy: str | None = None
    codex_all_proxy: str | None = None
    no_proxy: str = "127.0.0.1,localhost"

    guard_host: str = "127.0.0.1"
    guard_port: int = 8765
    request_timeout_seconds: int = 300
    ask_timeout_seconds: int = 600
    device_login_timeout_seconds: int = 900
    capability_ttl_seconds: int = 900
    tool_cache_ttl_seconds: int = 300
    max_file_bytes: int = 20 * 1024 * 1024
    max_history_items: int = 50
    default_timezone: str = "Europe/Vilnius"
    poll_interval_seconds: int = 60
    agenda_hour: int = 8
    codex_model: str | None = None
    codex_reasoning_effort: str = "medium"

    @field_validator(
        "telegram_proxy",
        "calendar_proxy",
        "codex_http_proxy",
        "codex_https_proxy",
        "codex_all_proxy",
        "codex_model",
        mode="before",
    )
    @classmethod
    def empty_string_to_none(cls, value: object) -> object:
        if isinstance(value, str) and not value.strip():
            return None
        return value

    @model_validator(mode="after")
    def derive_data_paths(self) -> "Settings":
        # Explicit STATE_FILE/WORK_DIR/CODEX_DIR still win. Otherwise every path
        # follows DATA_DIR instead of being pinned to /data independently.
        if self.state_file is None:
            self.state_file = self.data_dir / "state.json"
        if self.work_dir is None:
            self.work_dir = self.data_dir / "work"
        if self.codex_dir is None:
            self.codex_dir = self.data_dir / "codex"
        if self.ask_timeout_seconds <= 0:
            raise ValueError("ASK_TIMEOUT_SECONDS must be > 0")
        if self.capability_ttl_seconds <= 0:
            raise ValueError("CAPABILITY_TTL_SECONDS must be > 0")
        if max(self.ask_timeout_seconds, self.request_timeout_seconds) >= self.capability_ttl_seconds:
            raise ValueError("CAPABILITY_TTL_SECONDS must be greater than both ASK_TIMEOUT_SECONDS and REQUEST_TIMEOUT_SECONDS")
        return self

    @property
    def admins(self) -> set[int]:
        return {int(x.strip()) for x in self.admin_ids.split(",") if x.strip()}

    def ensure_dirs(self) -> None:
        assert self.state_file is not None and self.work_dir is not None and self.codex_dir is not None
        for p in (self.data_dir, self.state_file.parent, self.work_dir, self.codex_dir, self.cap_dir):
            p.mkdir(parents=True, exist_ok=True)
