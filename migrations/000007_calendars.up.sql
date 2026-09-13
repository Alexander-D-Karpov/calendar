CREATE TABLE calendars (
                           id                 uuid PRIMARY KEY,
                           owner_id           uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                           name               text NOT NULL,
                           color              text NOT NULL DEFAULT '#3b6ea5',
                           description        text NOT NULL DEFAULT '',
                           timezone           text,
                           kind               text NOT NULL DEFAULT 'local',
                           read_only          boolean NOT NULL DEFAULT false,
                           is_default         boolean NOT NULL DEFAULT false,
                           hidden             boolean NOT NULL DEFAULT false,
                           position           integer NOT NULL DEFAULT 0,
                           default_reminders  integer[] NOT NULL DEFAULT '{}',
                           created_at         timestamptz NOT NULL DEFAULT now(),
                           updated_at         timestamptz NOT NULL DEFAULT now(),
                           CONSTRAINT calendars_id_owner_key UNIQUE (id, owner_id),
                           CONSTRAINT calendars_name_check CHECK (char_length(name) BETWEEN 1 AND 100),
                           CONSTRAINT calendars_color_check CHECK (color ~ '^#[0-9a-f]{6}$'),
                           CONSTRAINT calendars_description_check CHECK (char_length(description) <= 2000),
                           CONSTRAINT calendars_kind_check CHECK (kind IN ('local', 'subscription')),
                           CONSTRAINT calendars_default_reminders_check CHECK (
                               cardinality(default_reminders) <= 5
                                   AND 0 <= ALL (default_reminders)
                                   AND 40320 >= ALL (default_reminders)
                               ),
                           CONSTRAINT calendars_default_check CHECK (NOT is_default OR (kind = 'local' AND NOT read_only))
);

CREATE UNIQUE INDEX calendars_owner_default_idx ON calendars (owner_id) WHERE is_default;
CREATE INDEX calendars_owner_position_idx ON calendars (owner_id, position);