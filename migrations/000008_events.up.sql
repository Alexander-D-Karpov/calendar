CREATE TABLE events (
                        id             uuid PRIMARY KEY,
                        owner_id       uuid NOT NULL,
                        calendar_id    uuid NOT NULL,
                        ical_uid       text NOT NULL,
                        series_id      uuid,
                        recurrence_id  timestamptz,
                        title          text NOT NULL DEFAULT '',
                        body           text NOT NULL DEFAULT '',
                        location       text NOT NULL DEFAULT '',
                        url            text NOT NULL DEFAULT '',
                        all_day        boolean NOT NULL DEFAULT false,
                        start_at       timestamptz,
                        end_at         timestamptz,
                        start_date     date,
                        end_date       date,
                        tz             text,
                        span           tstzrange NOT NULL,
                        rrule          text,
                        rdate          timestamptz[] NOT NULL DEFAULT '{}',
                        exdate         timestamptz[] NOT NULL DEFAULT '{}',
                        status         text NOT NULL DEFAULT 'confirmed',
                        transparency   text NOT NULL DEFAULT 'opaque',
                        visibility     text NOT NULL DEFAULT 'default',
                        color          text,
                        read_only      boolean NOT NULL DEFAULT false,
                        sequence       integer NOT NULL DEFAULT 0,
                        version        bigint NOT NULL DEFAULT 1,
                        fingerprint    bytea,
                        source_hash    bytea,
                        created_at     timestamptz NOT NULL DEFAULT now(),
                        updated_at     timestamptz NOT NULL DEFAULT now(),
                        deleted_at     timestamptz,
                        CONSTRAINT events_id_calendar_key UNIQUE (id, calendar_id),
                        CONSTRAINT events_calendar_fkey FOREIGN KEY (calendar_id, owner_id)
                            REFERENCES calendars (id, owner_id) ON DELETE CASCADE,
                        CONSTRAINT events_series_fkey FOREIGN KEY (series_id, calendar_id)
                            REFERENCES events (id, calendar_id) ON DELETE CASCADE ON UPDATE CASCADE,
                        CONSTRAINT events_timing_check CHECK (
                            (
                                all_day
                                    AND start_date IS NOT NULL AND end_date IS NOT NULL
                                    AND start_at IS NULL AND end_at IS NULL
                                    AND end_date > start_date
                                )
                                OR
                            (
                                NOT all_day
                                    AND start_at IS NOT NULL AND end_at IS NOT NULL
                                    AND start_date IS NULL AND end_date IS NULL
                                    AND end_at >= start_at
                                    AND tz IS NOT NULL
                                )
                            ),
                        CONSTRAINT events_span_check CHECK (NOT isempty(span)),
                        CONSTRAINT events_override_check CHECK ((series_id IS NULL) = (recurrence_id IS NULL)),
                        CONSTRAINT events_override_rules_check CHECK (
                            series_id IS NULL OR (rrule IS NULL AND cardinality(rdate) = 0 AND cardinality(exdate) = 0)
                            ),
                        CONSTRAINT events_series_self_check CHECK (series_id IS NULL OR series_id <> id),
                        CONSTRAINT events_status_check CHECK (status IN ('confirmed', 'tentative', 'cancelled')),
                        CONSTRAINT events_transparency_check CHECK (transparency IN ('opaque', 'transparent')),
                        CONSTRAINT events_visibility_check CHECK (visibility IN ('default', 'private')),
                        CONSTRAINT events_color_check CHECK (color IS NULL OR color ~ '^#[0-9a-f]{6}$'),
                        CONSTRAINT events_uid_check CHECK (char_length(ical_uid) BETWEEN 1 AND 1000),
                        CONSTRAINT events_title_check CHECK (char_length(title) <= 1000),
                        CONSTRAINT events_body_check CHECK (char_length(body) <= 100000),
                        CONSTRAINT events_location_check CHECK (char_length(location) <= 1000),
                        CONSTRAINT events_url_length_check CHECK (char_length(url) <= 2048),
                        CONSTRAINT events_url_check CHECK (url = '' OR url ~* '^https?://[^\s/?#]+([/?#]\S*)?$'),
                        CONSTRAINT events_rrule_check CHECK (rrule IS NULL OR char_length(rrule) BETWEEN 1 AND 1000),
                        CONSTRAINT events_dates_limit_check CHECK (cardinality(rdate) <= 1000 AND cardinality(exdate) <= 5000)
);

CREATE INDEX events_calendar_span_idx ON events USING gist (calendar_id, span) WHERE deleted_at IS NULL;
CREATE INDEX events_calendar_id_idx ON events (calendar_id);
CREATE UNIQUE INDEX events_calendar_uid_idx ON events (calendar_id, ical_uid)
    WHERE series_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX events_series_recurrence_idx ON events (series_id, recurrence_id)
    WHERE deleted_at IS NULL;
CREATE INDEX events_series_idx ON events (series_id, calendar_id) WHERE series_id IS NOT NULL;
CREATE INDEX events_owner_uid_idx ON events (owner_id, ical_uid);
CREATE INDEX events_owner_fingerprint_idx ON events (owner_id, fingerprint)
    WHERE fingerprint IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX events_owner_deleted_idx ON events (owner_id, deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE event_reminders (
                                 event_id        uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
                                 minutes_before  integer NOT NULL,
                                 PRIMARY KEY (event_id, minutes_before),
                                 CONSTRAINT event_reminders_minutes_check CHECK (minutes_before BETWEEN 0 AND 40320)
);