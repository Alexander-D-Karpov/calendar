# Calendar Codex Telegram Bot

A self-hosted Telegram control plane for ACalendar using **Codex with a ChatGPT subscription**, not an OpenAI API key.

## What it does

- aiogram Telegram bot, Dockerized.
- Multi-user allowlist with `user` / `admin` roles.
- Each Telegram user links their own ACalendar `cal_...` bearer token with `/link`.
- Each Telegram user logs their own Codex/ChatGPT subscription in from Telegram using `/codex_login` (device-code flow), or imports an existing `auth.json` with `/codex_session`.
- One active Codex request per user. If it is running, later messages queue; if Codex is explicitly waiting for clarification, the next ordinary message is treated as that answer and continues the same in-RAM ephemeral thread. An unanswered clarification expires after `ASK_TIMEOUT_SECONDS`.
- Request context is not persisted as a transcript. At `done`, the Codex client/thread and request workdir are destroyed. A restart aborts unfinished ephemeral requests.
- A compact completed-request history (request/final answer only, bounded) and explicit `/memory` entries are persisted **encrypted at rest**. The agent can search decrypted snippets only through `search_bot_context` when needed. No unfinished transcript is persisted.
- Photos and documents are downloaded into a per-request workdir; images are also passed as Codex `LocalImageInput`. PDF/docx/xlsx helper libraries/utilities are installed in the image.
- ACalendar is accessed through MCP only.
- Calendar bearer tokens are Fernet-encrypted in the flat JSON state and are **never** given to the Codex process. Codex gets a random, short-lived localhost MCP capability. That capability is handed to the bridge through a one-shot `0600` file under `/run`, not argv or environment; the bridge reads and unlinks it before serving MCP.
- No DB. `state.json` is loaded at startup and atomically rewritten after mutations.
- HTTP/SOCKS proxy support for Telegram, ACalendar MCP, and Codex.
- Polls `/changes` through MCP and pushes externally-originated changes. Sends a direct, no-LLM morning agenda once per day.

## Destructive-operation policy

The restriction is enforced outside the model:

1. `deleteCalendar`, `deleteTodoList`, purge/empty-trash/duplicate-resolution/bulk-delete-like tools are hidden and rejected.
2. At most one destructive entry operation is allowed per request capability: one `deleteEvent`, one `deleteTodo`, or one `deleteCheck`.
3. `deleteEvent` rejects recurring `scope=all` and `scope=following`. A recurring series can only delete one occurrence with `scope=this` plus an exact `instance`.
4. `deleteTodo` first calls `listTodos(parent_id=<id>)`; it rejects deletion when subtasks exist because ACalendar cascades the delete. `deleteCheck` is allowed because it removes exactly one checklist item.
5. `updateEvent` is **not** subject to the delete cap. Recurring edits may use `scope=this`, `scope=following`, or `scope=all` when that matches the user request.
6. Account-setting writes, public-share creation/update/regeneration, imports, duplicate resolution/scan, and remote-subscription create/update/refresh are hidden by the guard. `revokeShare` is allowed because it reduces exposure. Generic subscription deletion remains unavailable through the guard.

This is stricter than a prompt-only rule.

## Deploy

```bash
cp .env.example .env
python - <<'PY'
from cryptography.fernet import Fernet
print(Fernet.generate_key().decode())
PY
# Put that value in STATE_ENCRYPTION_KEY, add TELEGRAM_BOT_TOKEN + ADMIN_IDS.

docker compose build
# Mandatory deployment/runtime verification. This is deliberately not a Docker
# build layer because Codex initialization may depend on your deployment proxy/runtime.
docker compose run --rm calendar-bot python -m app.runtime_smoke --mode all
docker compose up -d
docker compose logs -f calendar-bot
```

### Seccomp

Compose runs the container under `seccomp-userns.json`, a copy of Docker's
default profile that additionally permits the namespace syscalls. Codex
sandboxes every request with `bwrap`, and under the stock profile that fails
with `bwrap: No permissions to create a new namespace`, so no request can run.
The profile is committed; regenerate it with `gen-seccomp.py` after a Docker
daemon upgrade. Everything the default profile denies unconditionally —
`init_module`, `bpf`, `perf_event_open`, `kexec_load` and the rest — stays
denied, and the generator refuses to write a profile where that stops being
true. This is a real widening of the container boundary relative to stock
Docker; it buys back the in-container Codex sandbox, which otherwise cannot
start at all.

The first admin IDs from `ADMIN_IDS` are force-bootstrapped on every startup. Docker Compose also applies a 4 GiB memory cap, 2 CPU cap and 512 PID cap to bound runaway Codex processes; tune these if your server needs different limits. The capability directory is a `tmpfs`, so one-shot capability files do not land in the container writable layer under normal Compose deployment.

## First-time Telegram setup

```text
/user_add 123456789 user       # admin does this
/link cal_...
/codex_login
/timezone Europe/Vilnius
/status
```

Then just message the bot, for example:

```text
move tomorrow's dentist to 17:00
add buy chain lube to todos for Saturday
what do I have next Monday?
delete my 10am standup tomorrow
```

If something material is ambiguous, Codex answers with a question. Your next message resumes the same request context.

### Import an existing Codex session instead

Run `/codex_session`, then send the user's own Codex `auth.json` as a Telegram document. The bot downloads it, deletes the Telegram credential message best-effort immediately, validates JSON, saves it as `/data/codex/<telegram_id>/auth.json` with `0600`, and verifies it by asking Codex for account state.

Device-code login is preferred because Codex owns the login/refresh lifecycle and avoids transporting `auth.json` through Telegram.

## Commands

User commands:

```text
/help
/status
/link cal_...
/unlink
/codex_login
/codex_session
/codex_status
/codex_logout
/timezone Europe/Vilnius
/poll on|off
/cancel
/memory add <text>
/memory list
/memory clear
```

Admin commands:

```text
/users
/user_add <telegram_id> [user|admin]
/user_role <telegram_id> <user|admin>
/user_del <telegram_id>
```

## Proxy configuration

All are optional:

```dotenv
TELEGRAM_PROXY=socks5://user:pass@proxy:1080
CALENDAR_PROXY=http://proxy:3128
CODEX_HTTP_PROXY=http://proxy:3128
CODEX_HTTPS_PROXY=http://proxy:3128
CODEX_ALL_PROXY=socks5://proxy:1080
NO_PROXY=127.0.0.1,localhost
```

Keep the local guard bound to `127.0.0.1`; do not publish `GUARD_PORT` from Docker.

## Flat state vs Codex credentials

`/data/state.json` contains users, encrypted calendar tokens, polling cursors, and encrypted compact request history / explicit memories. It never contains Codex credentials. `DATA_DIR` relocates state, work and Codex homes together unless `STATE_FILE`, `WORK_DIR` or `CODEX_DIR` are explicitly set.

The short-lived calendar capability is stored only long enough for the MCP bridge to start:

```text
/run/calendar-codex-bot/caps/<telegram_id>-<random>.cap   # 0600, immediately consumed + unlinked
```

The capability value is absent from `config.toml`, process argv, and the Codex environment. Session startup fails closed if the bridge does not consume the file.

Codex credentials live under per-user homes:

```text
/data/codex/<telegram_id>/auth.json
/data/codex/<telegram_id>/config.toml
```

A single Codex login is **not** shared across Telegram users. Every user authenticates their own subscription.

## Token-cost choices

- `web_search = "disabled"`.
- One MCP server only.
- ACalendar itself scope-filters `tools/list`; mint narrow tokens to reduce tool-schema context. For the intended bot, `calendars:write` + `todos:write` are normally sufficient (write includes read). Do **not** grant `shares:write`, `imports:write` or `account:write` unless another client actually needs them.
- The bot caches `tools/list` briefly (`TOOL_CACHE_TTL_SECONDS`) and resolves poller tool names from the actual MCP list instead of assuming operation IDs are MCP names.
- Files are passed by local path instead of injecting contents into the prompt.
- Old context is searched only on demand.
- Morning agenda and change notifications bypass the LLM entirely.
- The result is constrained with a small JSON output schema.

## Notes / operational boundaries

- The Codex runtime itself receives only a small allowlist of OS environment variables plus the explicitly configured Codex proxy variables. Telegram/state secrets are not inherited. Codex-spawned shell commands additionally inherit only the runtime's `core` environment, so authenticated proxy URLs are not intentionally exposed to shell tools.
- Model-command permissions use a dedicated Codex profile: `:root` is denied, `:minimal` is read-only, only the current request workspace is readable, and command network access is disabled. Calendar/todo writes happen only through the guarded MCP server.
- The Codex profile is selected as `default_permissions = "calendar_bot"` in the per-user `config.toml`. It is deliberately **not** duplicated through a second thread-level permissions path, and the project does **not** also pass the legacy sandbox preset because current Codex treats permission profiles and the legacy sandbox mechanism as separate/mutually-exclusive controls.
- **Shared Codex sessions:** an admin may lend their Codex login to another user with `/codex_grant <telegram_id>` (`/codex_revoke`, `/codex_grants`). The borrower's requests then run on the admin's ChatGPT account and against the admin's quota, and anything the model does is attributed to that account. Calendar access is unaffected: each user still links their own token, so a shared session grants model access, never calendar data. The credentials are copied into the borrower's own `CODEX_HOME` rather than shared in place, because `config.toml` there is request-scoped and two users pointing at one home would let a second request rewrite the config of a live one. The copy is refreshed from the lender on every request, so a re-login propagates on its own, and a marker file keeps a borrowed copy distinguishable from a real login so a grant never overwrites, and a revoke never deletes, credentials the user owns. `/codex_logout` on a borrowed session only drops the local copy; it deliberately does not call Codex's logout, which would invalidate the lender's credentials for everyone. **Two `CODEX_HOME`s using one OAuth credential can rotate its refresh token**; if the lender finds themselves logged out after lending, that is the cause, and the fix is a separate account per user rather than a shared session.
- **Multi-user trust boundary:** all users' Codex runtimes still execute as the same Unix UID (`bot`) inside one container. The Codex permission profile plus one-shot capability file are defense-in-depth, not kernel/user-namespace isolation. The built-in multi-user mode is intended for an allowlist of users you trust not to deliberately attack the host/runtime. For mutually untrusted users, run one bot/container (or otherwise one OS/container security boundary) per user.
- Runtime smoke is a **deployment/CI step, not a Docker build layer**. Run `docker compose run --rm calendar-bot python -m app.runtime_smoke --mode all` before first startup and after changing pinned SDK/runtime versions. It verifies: real MCP v2 stdio bridge wiring; structured-content round-trip; one-shot cap unlink; that `thread_start` eagerly launches the configured MCP bridge; rejection of an undefined permission profile; and rejection of an invalid inner permission-profile `extends`.
- One-shot capability delivery intentionally depends on eager MCP startup during `thread_start`. The runtime smoke turns that assumption into a hard deployment check. If a future Codex runtime defers MCP startup to first tool use, **do not deploy this build** until capability delivery is redesigned. Likewise, an MCP subprocess restart during a live thread cannot reuse the consumed one-shot file; that request fails closed rather than silently recreating access.
- Measured against `openai-codex==0.154.0`, `thread_start` launches the MCP server itself but returns without waiting for the child, so the capability is unlinked roughly 0.4s *after* the call returns rather than before it. The smoke therefore polls for the unlink up to `EAGER_MCP_STARTUP_TIMEOUT` instead of asserting it instantly; because it never issues a tool call, a runtime that defers MCP startup to first use still fails the check. The residual exposure is a sub-second window in which the `0600` capability file exists in the `tmpfs` cap directory, which the Codex filesystem profile does not grant the model read access to.
- For file formats Codex cannot interpret directly, the image includes `pypdf`, `python-docx`, `openpyxl`, Pillow, `poppler-utils`, `file`, and `unzip` so it can inspect local attachments without modifying them.
- `MAX_FILE_BYTES` is the application-side cap. Telegram/Bot API deployment limits may impose a lower or different effective limit.
- Morning agenda resolves `listEvents` and `listTodos` from the token's actual `tools/list`; change polling resolves `listChanges` the same way. Missing/ambiguous tools are logged and that polling feature is skipped. ACalendar currently documents all three operation IDs, with `/changes` requiring `calendars:read`.
- `CAPABILITY_TTL_SECONDS` must be greater than both `REQUEST_TIMEOUT_SECONDS` and `ASK_TIMEOUT_SECONDS`. The guard refreshes the capability at every turn and when entering/leaving clarification state.
- The bot intentionally does not persist an unfinished Codex transcript. Therefore an unfinished clarification flow cannot resume after a container restart.

## Tests

```bash
pip install -r requirements.txt pytest
pytest -q
python -m compileall -q app tests
python -m app.runtime_smoke --mode all
```

`runtime_smoke` does not require a Telegram token, calendar token, or Codex login. It checks installed dependency/runtime surfaces that pure unit tests cannot prove. Run it in the built Compose service so the same proxy/runtime settings as deployment are available; it is intentionally not executed during `docker build`.

## Sources used to pin behavior

- ACalendar API docs: https://calendar.akarpov.ru/api/docs/
- ACalendar OpenAPI: https://calendar.akarpov.ru/api/openapi.json
- Codex Python SDK API: https://github.com/openai/codex/blob/main/sdk/python/docs/api-reference.md
- Codex Python SDK getting started: https://github.com/openai/codex/blob/main/sdk/python/docs/getting-started.md
- MCP Python SDK: https://github.com/modelcontextprotocol/python-sdk
- aiogram aiohttp session/proxy docs: https://docs.aiogram.dev/en/latest/api/session/aiohttp.html
