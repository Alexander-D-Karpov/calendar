from __future__ import annotations

import asyncio
import json
import os
from copy import deepcopy
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable, TYPE_CHECKING

if TYPE_CHECKING:
    from .crypto import SecretBox


DEFAULT_USER = {
    "role": "user",
    "enabled": True,
    "calendar_token": None,
    "timezone": None,
    "poll_enabled": True,
    "change_cursor": None,
    "last_agenda_date": None,
    "history": [],
    "memories": [],
}


class Store:
    def __init__(
        self,
        path: Path,
        max_history_items: int = 50,
        secret_box: "SecretBox | None" = None,
    ) -> None:
        self.path = path
        self.max_history_items = max_history_items
        self.secret_box = secret_box
        self._lock = asyncio.Lock()
        self.data: dict[str, Any] = {"version": 2, "users": {}}

    async def load(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        if not self.path.exists():
            await self._dump()
            return
        raw = await asyncio.to_thread(self.path.read_text, encoding="utf-8")
        parsed = json.loads(raw)
        if not isinstance(parsed, dict) or not isinstance(parsed.get("users", {}), dict):
            raise RuntimeError("Invalid state file")
        self.data = parsed
        old_version = self.data.get("version", 1)
        self.data.setdefault("version", 1)
        self.data.setdefault("users", {})
        for uid in list(self.data["users"]):
            self.data["users"][uid] = self._normalize_user(self.data["users"][uid])

        migrated = self._migrate_sensitive_fields()
        self.data["version"] = 2
        if migrated or old_version != 2:
            await self._dump()

    def _normalize_user(self, user: dict[str, Any] | None) -> dict[str, Any]:
        out = deepcopy(DEFAULT_USER)
        if user:
            out.update(user)
        out["history"] = list(out.get("history") or [])[-self.max_history_items :]
        out["memories"] = list(out.get("memories") or [])[-100:]
        return out

    def _encrypt_text(self, value: str) -> str:
        if self.secret_box is None:
            return value
        return self.secret_box.encrypt(value)

    def _decrypt_text(self, value: str) -> str:
        if self.secret_box is None:
            return value
        return self.secret_box.decrypt(value)

    def _migrate_sensitive_fields(self) -> bool:
        if self.secret_box is None:
            return False
        changed = False
        for user in self.data.get("users", {}).values():
            history_out: list[dict[str, Any]] = []
            for item in user.get("history") or []:
                if not isinstance(item, dict):
                    changed = True
                    continue
                if "request_enc" in item and "result_enc" in item:
                    history_out.append(item)
                    continue
                req = str(item.get("request") or "")
                res = str(item.get("result") or "")
                history_out.append({
                    "at": item.get("at"),
                    "request_enc": self._encrypt_text(req),
                    "result_enc": self._encrypt_text(res),
                })
                changed = True
            user["history"] = history_out[-self.max_history_items :]

            memories_out: list[Any] = []
            for item in user.get("memories") or []:
                if isinstance(item, dict) and "text_enc" in item:
                    memories_out.append(item)
                else:
                    memories_out.append({"text_enc": self._encrypt_text(str(item))})
                    changed = True
            user["memories"] = memories_out[-100:]
        return changed

    async def _dump(self) -> None:
        async with self._lock:
            await self._dump_locked()

    async def _dump_locked(self) -> None:
        payload = json.dumps(self.data, ensure_ascii=False, indent=2, sort_keys=True)
        tmp = self.path.with_suffix(self.path.suffix + ".tmp")

        def write_atomic() -> None:
            with open(tmp, "w", encoding="utf-8") as f:
                f.write(payload)
                f.flush()
                os.fsync(f.fileno())
            os.replace(tmp, self.path)
            try:
                os.chmod(self.path, 0o600)
            except PermissionError:
                pass

        await asyncio.to_thread(write_atomic)

    def get_user(self, user_id: int) -> dict[str, Any] | None:
        user = self.data["users"].get(str(user_id))
        return deepcopy(user) if user else None

    def list_users(self) -> dict[str, dict[str, Any]]:
        return deepcopy(self.data["users"])

    def list_user_ids(self) -> list[int]:
        """Return user ids without deep-copying every user record."""
        out: list[int] = []
        for key in self.data["users"].keys():
            try:
                out.append(int(key))
            except (TypeError, ValueError):
                continue
        return out

    def is_allowed(self, user_id: int) -> bool:
        user = self.data["users"].get(str(user_id))
        return bool(user and user.get("enabled"))

    def is_admin(self, user_id: int) -> bool:
        user = self.data["users"].get(str(user_id))
        return bool(user and user.get("enabled") and user.get("role") == "admin")

    async def bootstrap_admins(self, admin_ids: set[int]) -> None:
        """Ensure configured bootstrap admins always exist, are enabled and stay admins."""
        if not admin_ids:
            raise RuntimeError("ADMIN_IDS must contain at least one Telegram user ID")
        async with self._lock:
            changed = False
            for user_id in admin_ids:
                key = str(user_id)
                current = self.data["users"].get(key)
                user = self._normalize_user(current)
                if current is None or user.get("role") != "admin" or not user.get("enabled"):
                    changed = True
                user["role"] = "admin"
                user["enabled"] = True
                self.data["users"][key] = user
            if changed:
                await self._dump_locked()

    async def ensure_user(self, user_id: int, role: str = "user") -> None:
        async with self._lock:
            key = str(user_id)
            if key not in self.data["users"]:
                user = self._normalize_user(None)
                user["role"] = role
                self.data["users"][key] = user
                await self._dump_locked()

    async def update_user(self, user_id: int, mutator: Callable[[dict[str, Any]], None]) -> None:
        async with self._lock:
            key = str(user_id)
            user = self._normalize_user(self.data["users"].get(key))
            mutator(user)
            self.data["users"][key] = user
            await self._dump_locked()

    async def remove_user(self, user_id: int) -> None:
        async with self._lock:
            self.data["users"].pop(str(user_id), None)
            await self._dump_locked()

    async def add_history(self, user_id: int, request: str, result: str) -> None:
        now = datetime.now(timezone.utc).isoformat()
        request = request[:1000]
        result = result[:1200]

        def mutate(user: dict[str, Any]) -> None:
            history = list(user.get("history") or [])
            if self.secret_box is None:
                item: dict[str, Any] = {"at": now, "request": request, "result": result}
            else:
                item = {
                    "at": now,
                    "request_enc": self._encrypt_text(request),
                    "result_enc": self._encrypt_text(result),
                }
            history.append(item)
            user["history"] = history[-self.max_history_items :]

        await self.update_user(user_id, mutate)

    async def add_memory(self, user_id: int, text: str) -> None:
        text = text[:2000]

        def mutate(user: dict[str, Any]) -> None:
            memories = list(user.get("memories") or [])
            memories.append(text if self.secret_box is None else {"text_enc": self._encrypt_text(text)})
            user["memories"] = memories[-100:]

        await self.update_user(user_id, mutate)

    def list_memories(self, user_id: int) -> list[str]:
        user = self.data["users"].get(str(user_id)) or {}
        out: list[str] = []
        for item in user.get("memories") or []:
            if isinstance(item, dict) and isinstance(item.get("text_enc"), str):
                try:
                    out.append(self._decrypt_text(item["text_enc"]))
                except Exception:
                    continue
            else:
                out.append(str(item))
        return out

    def search_context(self, user_id: int, query: str, limit: int = 5) -> list[dict[str, Any]]:
        user = self.data["users"].get(str(user_id)) or {}
        q = {w.lower() for w in query.split() if len(w) > 2}
        candidates: list[tuple[int, dict[str, Any]]] = []
        for item in user.get("history") or []:
            if not isinstance(item, dict):
                continue
            try:
                if "request_enc" in item:
                    req = self._decrypt_text(str(item.get("request_enc") or ""))
                    res = self._decrypt_text(str(item.get("result_enc") or ""))
                else:
                    req = str(item.get("request") or "")
                    res = str(item.get("result") or "")
            except Exception:
                continue
            text = f"{req} {res}".lower()
            score = sum(1 for w in q if w in text)
            if score or not q:
                candidates.append((score, {
                    "kind": "history",
                    "at": item.get("at"),
                    "request": req,
                    "result": res,
                }))
        for text in self.list_memories(user_id):
            low = text.lower()
            score = sum(1 for w in q if w in low)
            if score or not q:
                candidates.append((score + 1, {"kind": "memory", "text": text}))
        candidates.sort(key=lambda x: x[0], reverse=True)
        return [item for _, item in candidates[: max(1, min(limit, 10))]]
