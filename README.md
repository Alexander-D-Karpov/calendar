# Calendar

A calendar and todo server in Go. Server-rendered pages with a JSON API beside
them, Google Calendar and Tasks sync, iCal import, export and subscriptions,
share links with generated preview images, and web push reminders.

Runs at [calendar.akarpov.ru](https://calendar.akarpov.ru).

## Requirements

- Go 1.27
- PostgreSQL 17

## Running locally

```sh
cp .env.example .env
go run ./cmd/calendar keys generate     # paste SECRET_KEYS into .env
go run ./cmd/calendar migrate up
go run ./cmd/calendar serve
```

`config check` validates `.env` and prints what each setting turned on, which is
faster than starting the server to find out:

```sh
go run ./cmd/calendar config check
```

## Commands

| Command | |
| --- | --- |
| `serve` | HTTP server |
| `worker` | job queue: sync, reminders, subscriptions, retention |
| `migrate` | `up`, `down`, `status`, `goto`, `force` |
| `user` | `create`, `list`, `enable`, `disable`, `set-password` |
| `config` | `check`, `print` |
| `keys` | generate `SECRET_KEYS` |
| `vapid` | generate web push keys |
| `healthcheck` | probe used by the container |

`serve` and `worker` are separate processes on purpose. Running the queue inside
the web process lets a slow Google sync compete with page loads.

## Development

```sh
make help              # every target
make vet test          # what CI gates on
make test-integration  # needs TEST_DATABASE_URL
```

Three artefacts are generated and committed, and CI fails on any drift:
`internal/db/sqlc` from the migrations and queries (`make sqlc`),
`web/static/css/slots.css` (`make css`), and the vendored Swagger UI
(`make vendor-swagger`). Regenerate rather than hand-edit.

The API is described by `api/openapi.yaml`, and tests hold it to the routes: an
undocumented endpoint, a missing scope or a handler whose types drift from its
schema all fail the suite.

## Deploying

See [deploy/README.md](deploy/README.md).
