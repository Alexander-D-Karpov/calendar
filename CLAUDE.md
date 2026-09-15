# Working in this repo

Go calendar and todo server, deployed at https://calendar.akarpov.ru.

## Conventions

- No comments except where something non-obvious needs explaining. Existing
  comments say *why*, not *what*; match that.
- Imports at the top. Follow the package conventions already in use:
  `domain.Opt` for patches, `mapErr`/`affected` in the store, `recordChange`,
  `site.Fail`/`site.Retry`, `web.Option`.
- Every new template goes through `web.Renderer` and needs a case in
  `internal/web/render_test.go`, which is the only thing that parses them.
- Run `make vet && make test` after each change. `make test-integration` needs
  `TEST_DATABASE_URL`; locally that is
  `postgres://sanspie@localhost:5432/calendar_test?sslmode=disable`.

## Generated, never hand-edited

CI fails on drift in all three:

| Artefact | Regenerate |
| --- | --- |
| `internal/db/sqlc` | `make sqlc` (sqlc v1.31.1) |
| `web/static/css/slots.css` | `make css` |
| `web/static/vendor/swagger-ui` | `make vendor-swagger` |

`api/openapi.yaml` is held to the routes by tests: an undocumented endpoint, a
missing `x-scope`, or a handler whose types drift from its schema fails the
suite.

## CSS

Stylesheets are per-page, not bundled. `head.html` loads themes, base, layout,
buttons, forms, settings and picker; a page adds its own. A rule for a class
used on a page that does not load its stylesheet silently does nothing — this
has caused real bugs twice. Check which sheet a page loads before adding a rule.

Event blocks use container queries, not media queries: they shed the excerpt,
location and time as the block gets shorter or narrower. Add to that cascade
rather than introducing a breakpoint.

## Deploying

The server is `sanspie@akarpov.ru`, checkout at `~/calendar`. Full notes in
`deploy/README.md`.

```sh
ssh sanspie@akarpov.ru
cd ~/calendar && git pull
VERSION=$(git describe --tags --always) COMMIT=$(git rev-parse HEAD) DATE=$(date -u +%FT%TZ) \
  docker compose -f docker-compose.yml -f deploy/docker-compose.monitoring.yml up -d --build
```

The monitoring overlay is required on that host: it joins the app and worker to
the Prometheus network. Without it the containers still run, but they drop off
the dashboard.

Config changes are `.env` edits followed by `up -d`; check them with
`docker compose run --rm --no-deps app config check` before restarting.

`.env` holds `SECRET_KEYS`. A database dump without it leaves every Google
connection, share link and subscription unreadable.
