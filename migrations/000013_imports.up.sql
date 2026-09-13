CREATE TABLE imports (
                         id                  uuid PRIMARY KEY,
                         owner_id            uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                         source              text NOT NULL,
                         filename            text NOT NULL DEFAULT '',
                         status              text NOT NULL DEFAULT 'pending',
                         payload             bytea,
                         payload_sha256      bytea,
                         options             jsonb NOT NULL DEFAULT '{}'::jsonb,
                         preview             jsonb,
                         stats               jsonb,
                         error               text,
                         target_calendar_id  uuid,
                         target_list_id      uuid,
                         created_at          timestamptz NOT NULL DEFAULT now(),
                         updated_at          timestamptz NOT NULL DEFAULT now(),
                         expires_at          timestamptz NOT NULL,
                         finished_at         timestamptz,
                         CONSTRAINT imports_target_calendar_fkey FOREIGN KEY (target_calendar_id, owner_id)
                             REFERENCES calendars (id, owner_id) ON DELETE SET NULL (target_calendar_id),
                         CONSTRAINT imports_target_list_fkey FOREIGN KEY (target_list_id, owner_id)
                             REFERENCES todo_lists (id, owner_id) ON DELETE SET NULL (target_list_id),
                         CONSTRAINT imports_source_check CHECK (source IN ('ics', 'csv', 'yaml', 'ics_url')),
                         CONSTRAINT imports_status_check CHECK (
                             status IN ('pending', 'previewed', 'committing', 'done', 'failed', 'expired')
                             ),
                         CONSTRAINT imports_filename_check CHECK (char_length(filename) <= 255)
);

CREATE INDEX imports_owner_created_idx ON imports (owner_id, created_at DESC);
CREATE INDEX imports_expires_at_idx ON imports (expires_at) WHERE payload IS NOT NULL;