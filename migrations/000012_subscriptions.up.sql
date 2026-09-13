CREATE TABLE ics_subscriptions (
                                   id                uuid PRIMARY KEY,
                                   owner_id          uuid NOT NULL,
                                   calendar_id       uuid NOT NULL,
                                   url_enc           bytea NOT NULL,
                                   url_host          text NOT NULL,
                                   refresh_interval  interval NOT NULL,
                                   next_refresh_at   timestamptz NOT NULL DEFAULT now(),
                                   status            text NOT NULL DEFAULT 'ok',
                                   etag              text,
                                   last_modified     text,
                                   content_hash      bytea,
                                   failures          integer NOT NULL DEFAULT 0,
                                   last_attempt_at   timestamptz,
                                   last_ok_at        timestamptz,
                                   last_error        text,
                                   created_at        timestamptz NOT NULL DEFAULT now(),
                                   updated_at        timestamptz NOT NULL DEFAULT now(),
                                   CONSTRAINT ics_subscriptions_calendar_key UNIQUE (calendar_id),
                                   CONSTRAINT ics_subscriptions_calendar_fkey FOREIGN KEY (calendar_id, owner_id)
                                       REFERENCES calendars (id, owner_id) ON DELETE CASCADE,
                                   CONSTRAINT ics_subscriptions_status_check CHECK (status IN ('ok', 'error', 'paused')),
                                   CONSTRAINT ics_subscriptions_interval_check CHECK (refresh_interval >= interval '1 minute'),
                                   CONSTRAINT ics_subscriptions_failures_check CHECK (failures >= 0)
);

CREATE INDEX ics_subscriptions_due_idx ON ics_subscriptions (next_refresh_at) WHERE status <> 'paused';
CREATE INDEX ics_subscriptions_owner_idx ON ics_subscriptions (owner_id);