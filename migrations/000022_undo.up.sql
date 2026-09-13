CREATE TABLE undo_entries (
                              id          uuid PRIMARY KEY,
                              owner_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                              kind        text NOT NULL,
                              label       text NOT NULL DEFAULT '',
                              payload     jsonb NOT NULL,
                              created_at  timestamptz NOT NULL DEFAULT now(),
                              expires_at  timestamptz NOT NULL,
                              used_at     timestamptz,
                              CONSTRAINT undo_entries_kind_check CHECK (char_length(kind) BETWEEN 1 AND 64),
                              CONSTRAINT undo_entries_label_check CHECK (char_length(label) <= 200)
);

CREATE INDEX undo_entries_owner_idx ON undo_entries (owner_id, created_at DESC);
CREATE INDEX undo_entries_expires_idx ON undo_entries (expires_at);