from __future__ import annotations

import asyncio
import shutil
import uuid
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Awaitable, Callable, Protocol
from zoneinfo import ZoneInfo

from .codex_runtime import CodexRuntime, LiveCodexSession
from .crypto import SecretBox
from .guard import GuardServer
from .store import Store


@dataclass
class Incoming:
    chat_id: int
    text: str
    files: list[Path] = field(default_factory=list)
    image_paths: list[Path] = field(default_factory=list)


@dataclass
class ActiveRequest:
    request_id: str
    user_id: int
    chat_id: int
    initial_text: str
    workdir: Path
    cap: str
    session: LiveCodexSession
    state: str = "running"  # running | asking
    finished: bool = False
    done: asyncio.Event = field(default_factory=asyncio.Event)
    ask_task: asyncio.Task[None] | None = None


SendFn = Callable[[int, str], Awaitable[None]]


class LiveStatus(Protocol):
    async def update(self, kind: str, text: str) -> None: ...
    async def finish(self, text: str) -> None: ...
    async def discard(self) -> None: ...


StatusFn = Callable[[int], Awaitable[LiveStatus]]


class RequestManager:
    def __init__(
        self,
        store: Store,
        crypto: SecretBox,
        guard: GuardServer,
        codex: CodexRuntime,
        timeout: int,
        ask_timeout: int,
        send: SendFn,
        work_root: Path,
        default_timezone: str,
        status: StatusFn | None = None,
    ) -> None:
        self.status = status
        self.store = store
        self.crypto = crypto
        self.guard = guard
        self.codex = codex
        self.timeout = timeout
        self.ask_timeout = ask_timeout
        self.send = send
        self.work_root = work_root
        self.default_timezone = default_timezone
        self.active: dict[int, ActiveRequest] = {}
        self.queues: dict[int, asyncio.Queue[Incoming]] = {}
        self.workers: dict[int, asyncio.Task[None]] = {}
        self._locks: dict[int, asyncio.Lock] = {}
        self._queue_locks: dict[int, asyncio.Lock] = {}

    def has_active(self, user_id: int) -> bool:
        # Keep the request visible through teardown so login/status/config commands
        # cannot race its request-scoped Codex config.
        return user_id in self.active

    def waiting_for_answer(self, user_id: int) -> bool:
        req = self.active.get(user_id)
        return bool(req and not req.finished and req.state == "asking")

    async def submit(self, user_id: int, incoming: Incoming) -> None:
        # While Codex explicitly waits for clarification, the next ordinary message
        # is that answer and remains in the same ephemeral in-RAM thread.
        if self.waiting_for_answer(user_id):
            await self.answer(user_id, incoming)
            return

        q = self.queues.setdefault(user_id, asyncio.Queue())
        qlock = self._queue_locks.setdefault(user_id, asyncio.Lock())
        queued_notice = False
        async with qlock:
            await q.put(incoming)
            worker = self.workers.get(user_id)
            if not worker or worker.done():
                self.workers[user_id] = asyncio.create_task(self._worker(user_id))
            elif self.has_active(user_id):
                queued_notice = True
        if queued_notice:
            await self.send(incoming.chat_id, f"Queued ({q.qsize()} request(s) waiting).")

    async def _worker(self, user_id: int) -> None:
        q = self.queues[user_id]
        qlock = self._queue_locks.setdefault(user_id, asyncio.Lock())
        current = asyncio.current_task()
        while True:
            incoming = await q.get()
            try:
                req = await self._start(user_id, incoming)
                if req is not None:
                    # No polling loop: request completion is an explicit lifecycle event.
                    # An unanswered clarification is closed by _ask_watchdog().
                    await req.done.wait()
            except asyncio.CancelledError:
                req = self.active.get(user_id)
                if req and not req.finished:
                    await asyncio.shield(self._finish(req, "Cancelled", save_history=False))
                raise
            except Exception as exc:
                await self.send(incoming.chat_id, f"Request failed: {exc}")
            finally:
                self._cleanup_incoming(incoming)
                q.task_done()

            # Coordinate exit with submit() so a message cannot land between
            # q.empty() and worker termination and become stuck.
            async with qlock:
                if q.empty():
                    if self.workers.get(user_id) is current:
                        self.workers.pop(user_id, None)
                    return

    async def _start(self, user_id: int, incoming: Incoming) -> ActiveRequest | None:
        lock = self._locks.setdefault(user_id, asyncio.Lock())
        async with lock:
            user = self.store.get_user(user_id) or {}
            encrypted = user.get("calendar_token")
            if not encrypted:
                self._cleanup_incoming(incoming)
                await self.send(incoming.chat_id, "Calendar is not linked. Use /link cal_… first.")
                return None
            if not self.codex.auth_path(user_id).exists():
                self._cleanup_incoming(incoming)
                await self.send(incoming.chat_id, "Codex is not logged in. Use /codex_login first.")
                return None

            token = self.crypto.decrypt(encrypted)
            request_id = uuid.uuid4().hex
            workdir = self.work_root / str(user_id) / request_id
            workdir.mkdir(parents=True, exist_ok=True)

            cap: str | None = None
            try:
                for source in incoming.files:
                    if source.parent != workdir and source.exists():
                        dest = workdir / source.name
                        shutil.move(str(source), dest)
                self._cleanup_incoming(incoming)

                cap = await self.guard.create_capability(user_id, token)
                timezone = user.get("timezone") or self.default_timezone
                try:
                    now = datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
                except Exception:
                    timezone = self.default_timezone
                    now = datetime.now(ZoneInfo(timezone)).isoformat(timespec="seconds")
                session = await self.codex.new_session(
                    user_id,
                    workdir,
                    self.guard.base_url,
                    cap,
                    timezone,
                    now,
                )
            except BaseException:
                # Cancellation during startup must not strand a capability/workdir.
                if cap is not None:
                    self.guard.revoke(cap)
                shutil.rmtree(workdir, ignore_errors=True)
                raise

            assert cap is not None
            req = ActiveRequest(
                request_id=request_id,
                user_id=user_id,
                chat_id=incoming.chat_id,
                initial_text=incoming.text,
                workdir=workdir,
                cap=cap,
                session=session,
            )
            self.active[user_id] = req

        prompt = self._initial_prompt(incoming, workdir)
        await self._turn(req, prompt, self._image_paths(incoming, workdir))
        return req

    @staticmethod
    def _image_paths(incoming: Incoming, workdir: Path) -> list[Path]:
        names = {p.name for p in incoming.image_paths}
        return [workdir / name for name in names if (workdir / name).exists()]

    @staticmethod
    def _initial_prompt(incoming: Incoming, workdir: Path) -> str:
        file_names = sorted(p.name for p in workdir.iterdir() if p.is_file())
        suffix = ""
        if file_names:
            suffix = "\nAttached files (local paths, open only if needed):\n" + "\n".join(
                f"- {name}" for name in file_names
            )
        body = incoming.text or "Use the attached file(s) to infer and perform the requested calendar/todo action."
        return f"User request:\n{body}{suffix}"

    async def answer(self, user_id: int, incoming: Incoming) -> None:
        req = self.active.get(user_id)
        if not req or req.finished or req.state != "asking":
            await self.submit(user_id, incoming)
            return

        if req.ask_task and not req.ask_task.done():
            req.ask_task.cancel()
        req.ask_task = None
        req.state = "running"
        if not self.guard.touch(req.cap):
            self._cleanup_incoming(incoming)
            await self.send(req.chat_id, "Calendar capability expired; the request was cancelled.")
            await self._finish(req, final_text="Capability expired", save_history=False)
            return

        images: list[Path] = []
        added_names: list[str] = []
        try:
            for source in incoming.files:
                if source.exists():
                    dest = req.workdir / source.name
                    if source != dest:
                        shutil.move(str(source), dest)
                    added_names.append(dest.name)
                    if source in incoming.image_paths or dest.suffix.lower() in {".jpg", ".jpeg", ".png", ".webp"}:
                        images.append(dest)
        finally:
            self._cleanup_incoming(incoming)

        body = incoming.text or "See the newly attached file(s)."
        if added_names:
            body += "\nNew attached files:\n" + "\n".join(f"- {name}" for name in sorted(added_names))
        await self._turn(req, f"User answer to your question:\n{body}", images)

    @staticmethod
    async def _drop(live: LiveStatus | None) -> None:
        """Remove the progress message so an error is not left under a spinner."""
        if live is None:
            return
        try:
            await live.discard()
        except Exception:
            pass

    async def _turn(self, req: ActiveRequest, text: str, images: list[Path]) -> None:
        if req.finished:
            return
        if not self.guard.touch(req.cap):
            await self.send(req.chat_id, "Calendar capability expired; the request was cancelled.")
            await self._finish(req, final_text="Capability expired", save_history=False)
            return

        # A single message shows what the model is doing and then becomes the
        # answer, so a slow request is not silence.
        live = await self.status(req.chat_id) if self.status else None
        try:
            progress = {"on_progress": live.update} if live else {}
            response = await asyncio.wait_for(
                req.session.run(text, images, **progress), timeout=self.timeout
            )
        except asyncio.TimeoutError:
            if req.finished:
                return
            await self._drop(live)
            await self.send(req.chat_id, "Codex request timed out and was cancelled.")
            await self._finish(req, final_text="Timed out", save_history=False)
            return
        except asyncio.CancelledError:
            await self._drop(live)
            if not req.finished:
                await asyncio.shield(self._finish(req, final_text="Cancelled", save_history=False))
            raise
        except Exception as exc:
            if req.finished:
                return
            await self._drop(live)
            await self.send(req.chat_id, f"Codex failed: {exc}")
            await self._finish(req, final_text=str(exc), save_history=False)
            return

        if req.finished:
            await self._drop(live)
            return
        if live:
            await live.finish(response["text"])
        else:
            await self.send(req.chat_id, response["text"])
        if response["status"] == "ask":
            req.state = "asking"
            self.guard.touch(req.cap)
            req.ask_task = asyncio.create_task(self._ask_watchdog(req))
            return
        await self._finish(req, final_text=response["text"], save_history=True)

    async def _ask_watchdog(self, req: ActiveRequest) -> None:
        try:
            await asyncio.sleep(self.ask_timeout)
            if req.finished or self.active.get(req.user_id) is not req or req.state != "asking":
                return
            await self.send(req.chat_id, "No answer received; the request was dropped.")
            await self._finish(req, "Abandoned", save_history=False)
        except asyncio.CancelledError:
            return

    async def _finish(self, req: ActiveRequest, final_text: str, save_history: bool) -> None:
        # cancel(), timeout, question expiry and an in-flight runner failure can converge here.
        if req.finished:
            return
        req.finished = True
        ask_task = req.ask_task
        req.ask_task = None
        if ask_task and ask_task is not asyncio.current_task() and not ask_task.done():
            ask_task.cancel()

        try:
            self.guard.revoke(req.cap)
            try:
                await req.session.close()
            finally:
                shutil.rmtree(req.workdir, ignore_errors=True)
            if save_history:
                await self.store.add_history(req.user_id, req.initial_text, final_text)
        finally:
            if self.active.get(req.user_id) is req:
                self.active.pop(req.user_id, None)
            req.done.set()

    def _cleanup_incoming(self, incoming: Incoming) -> None:
        staging_root = (self.work_root / ".staging").resolve()
        parents: set[Path] = set()
        for path in incoming.files:
            parent = path.parent
            try:
                resolved = parent.resolve()
                if resolved.is_relative_to(staging_root):
                    parents.add(parent)
            except (OSError, RuntimeError):
                continue
        for parent in parents:
            shutil.rmtree(parent, ignore_errors=True)

    async def cancel(self, user_id: int) -> bool:
        changed = False
        req = self.active.get(user_id)
        if req and not req.finished:
            await self._finish(req, "Cancelled", save_history=False)
            changed = True

        q = self.queues.get(user_id)
        if not q:
            return changed
        qlock = self._queue_locks.setdefault(user_id, asyncio.Lock())
        async with qlock:
            while not q.empty():
                try:
                    incoming = q.get_nowait()
                except asyncio.QueueEmpty:
                    break
                self._cleanup_incoming(incoming)
                q.task_done()
                changed = True
        return changed

    async def shutdown(self) -> None:
        for req in list(self.active.values()):
            if not req.finished:
                await self._finish(req, "Server shutdown", save_history=False)
        tasks = list(self.workers.values())
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
