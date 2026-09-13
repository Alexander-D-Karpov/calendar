# Deploying

Two processes share one image: `app` serves requests, `worker` drains the job
queue. Keep `WORKER_ENABLED=false` on `app` so a slow Google sync never competes
with page loads.

## First deploy

Pick host ports before anything else. The defaults, 8080 and 9091, are taken on
any host already running services, and the container will simply fail to bind:

```sh
ss -ltn                                     # then set APP_HOST_PORT and
                                            # METRICS_HOST_PORT in .env
```

```sh
cp deploy/env.production.example .env      # then fill every <...>
./calendar keys generate                    # SECRET_KEYS
./calendar vapid generate                   # WEBPUSH_VAPID_PUBLIC / _PRIVATE
openssl rand -hex 24                        # METRICS_TOKEN

./calendar config check                     # refuses to start on a bad config
docker compose up -d --build
docker compose exec app /calendar migrate up
docker compose exec app /calendar user create --email you@akarpov.ru --password-stdin
```

Then the front door. `calendar.conf` is HTTP only; certbot rewrites it in place
to add the TLS block and the redirect, which is why DNS has to resolve first:

```sh
sudo cp deploy/nginx/calendar.conf /etc/nginx/sites-enabled/calendar.akarpov.ru.conf
sudo nginx -t && sudo systemctl reload nginx
curl -sI http://calendar.akarpov.ru/readyz          # 200 over plain HTTP

sudo certbot --nginx -d calendar.akarpov.ru
sudo nginx -t && sudo systemctl reload nginx
```

Do not add the `listen 443` block by hand first. It names a certificate that
does not exist until certbot has run, so `nginx -t` fails, the reload fails, and
certbot has no working nginx to modify.

`proxy_pass` must point at `APP_HOST_PORT`, not at 8080, which is the port
inside the container.

`DATABASE_AUTO_MIGRATE` defaults to true, so the first boot migrates on its own;
running `migrate up` first makes the step explicit and fails loudly instead of
mid-request.

Before pointing DNS at it, confirm:

```sh
docker compose ps                           # app and worker both healthy
curl -sI https://calendar.akarpov.ru/readyz # 200
docker compose logs app | grep -i warn      # config warnings are printed once at boot
```

## Upgrading

```sh
git pull && docker compose up -d --build
docker compose exec app /calendar migrate up
```

Migrations run up, down and up again in CI, so a rollback is exercised before it
is ever needed in production. Take a dump first anyway.

## Secrets

Generate each one before the first boot and put it in `.env`:

```sh
./calendar keys generate       # SECRET_KEYS
./calendar vapid generate      # WEBPUSH_VAPID_PUBLIC / WEBPUSH_VAPID_PRIVATE
openssl rand -hex 24           # METRICS_TOKEN
```

`SECRET_KEYS` encrypts Google refresh tokens, share tokens and subscription
URLs. Losing it does not lock you out of the account, but every one of those
values becomes unreadable.

## Google

In the Google Cloud console, for the OAuth client:

- Authorised redirect URI: `https://calendar.akarpov.ru/auth/google/callback`.
  Add `http://localhost:8080/auth/google/callback` too if you develop locally;
  `http` is only accepted for `localhost` and `127.0.0.1`, which Google treats
  as different hosts.
- Scopes: `openid`, `email`, `profile` for sign-in, plus
  `https://www.googleapis.com/auth/calendar` and `.../auth/tasks` for sync.
- For push notifications, verify the domain under Search Console and add it to
  the console's domain verification list, then confirm
  `https://calendar.akarpov.ru/hooks/google/calendar` is reachable. Without an
  https base URL the app falls back to polling on its own.

## nginx

Copy `deploy/nginx/calendar.conf` and set `HTTP_TRUSTED_PROXIES` to the address
nginx connects from. If you skip that, every request looks like it came from the
proxy, so all per-IP rate limits collapse into one shared bucket.

## Backup

A database dump alone is not a backup. Restoring without `SECRET_KEYS` leaves
every Google connection, share link and subscription unreadable.

```sh
docker compose exec -T db pg_dump -U calendar calendar | gzip > calendar-$(date +%F).sql.gz
cp .env calendar-env-$(date +%F).bak     # holds SECRET_KEYS
```

Practise the restore before you need it:

```sh
gunzip -c calendar-2026-09-13.sql.gz | docker compose exec -T db psql -U calendar calendar
docker compose exec app /calendar config check
```

`config check` fails loudly if the keys no longer open the stored ciphertexts.

## Rotating SECRET_KEYS

`KeyRing` holds every key it is given, so old ciphertexts keep opening while new
writes use the active one. Rotate in two deploys, never one:

1. Append a new entry to `SECRET_KEYS` and deploy. Nothing changes yet.
2. Point `SECRET_KEY_ACTIVE` at the new entry and deploy again.

Keep the retired key in `SECRET_KEYS` until you are sure nothing still holds
data encrypted under it. Removing it too early is the same as losing it.
