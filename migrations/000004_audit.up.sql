CREATE TABLE audit_log (
                           id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
                           user_id     uuid REFERENCES users (id) ON DELETE CASCADE,
                           action      text NOT NULL,
                           ip          inet,
                           user_agent  text NOT NULL DEFAULT '',
                           meta        jsonb NOT NULL DEFAULT '{}'::jsonb,
                           created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_user_created_idx ON audit_log (user_id, created_at DESC);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at);