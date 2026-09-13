CREATE TABLE sessions (
                          id                   uuid PRIMARY KEY,
                          token_hash           bytea NOT NULL,
                          user_id              uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                          csrf_secret          bytea NOT NULL,
                          ip                   inet,
                          user_agent           text NOT NULL DEFAULT '',
                          created_at           timestamptz NOT NULL DEFAULT now(),
                          last_seen_at         timestamptz NOT NULL DEFAULT now(),
                          expires_at           timestamptz NOT NULL,
                          absolute_expires_at  timestamptz NOT NULL,
                          reauth_at            timestamptz,
                          CONSTRAINT sessions_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE email_tokens (
                              id          uuid PRIMARY KEY,
                              user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                              purpose     text NOT NULL,
                              token_hash  bytea NOT NULL,
                              new_email   citext,
                              created_at  timestamptz NOT NULL DEFAULT now(),
                              expires_at  timestamptz NOT NULL,
                              used_at     timestamptz,
                              CONSTRAINT email_tokens_purpose_check CHECK (purpose IN ('verify', 'reset', 'email_change')),
                              CONSTRAINT email_tokens_token_hash_key UNIQUE (token_hash),
                              CONSTRAINT email_tokens_new_email_check CHECK ((purpose = 'email_change') = (new_email IS NOT NULL))
);

CREATE INDEX email_tokens_user_purpose_idx ON email_tokens (user_id, purpose);
CREATE INDEX email_tokens_expires_at_idx ON email_tokens (expires_at);

CREATE TABLE api_tokens (
                            id            uuid PRIMARY KEY,
                            user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                            name          text NOT NULL,
                            prefix        text NOT NULL,
                            secret_hash   bytea NOT NULL,
                            scopes        text[] NOT NULL,
                            created_at    timestamptz NOT NULL DEFAULT now(),
                            expires_at    timestamptz,
                            last_used_at  timestamptz,
                            last_used_ip  inet,
                            revoked_at    timestamptz,
                            CONSTRAINT api_tokens_prefix_key UNIQUE (prefix),
                            CONSTRAINT api_tokens_name_check CHECK (char_length(name) BETWEEN 1 AND 100),
                            CONSTRAINT api_tokens_scopes_check CHECK (cardinality(scopes) > 0)
);

CREATE INDEX api_tokens_user_id_idx ON api_tokens (user_id);

CREATE TABLE idempotency_keys (
                                  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                  key            text NOT NULL,
                                  method         text NOT NULL,
                                  path           text NOT NULL,
                                  request_hash   bytea NOT NULL,
                                  status         smallint,
                                  response_type  text,
                                  response_body  bytea,
                                  created_at     timestamptz NOT NULL DEFAULT now(),
                                  PRIMARY KEY (user_id, key),
                                  CONSTRAINT idempotency_keys_key_check CHECK (char_length(key) BETWEEN 1 AND 255)
);

CREATE INDEX idempotency_keys_created_at_idx ON idempotency_keys (created_at);