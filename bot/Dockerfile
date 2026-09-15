FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates file poppler-utils unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt ./
RUN pip install --upgrade pip && pip install -r requirements.txt

RUN useradd --create-home --uid 10001 bot \
    && mkdir -p /data /run/calendar-codex-bot/caps \
    && chown -R bot:bot /data /app /run/calendar-codex-bot
COPY --chown=bot:bot app ./app

USER bot

# Runtime smoke tests are intentionally NOT a build layer. They exercise the
# installed Codex app-server/MCP startup behavior and may require deployment
# proxy/runtime conditions. Run them with docker compose before first startup.
VOLUME ["/data"]
CMD ["python", "-m", "app.main"]
