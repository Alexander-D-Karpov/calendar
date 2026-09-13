CREATE TABLE google_accounts (
                                 id                   uuid PRIMARY KEY,
                                 user_id              uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                 identity_id          uuid NOT NULL REFERENCES user_identities (id) ON DELETE CASCADE,
                                 refresh_token_enc    bytea NOT NULL,
                                 scopes               text[] NOT NULL DEFAULT '{}',
                                 status               text NOT NULL DEFAULT 'ok',
                                 last_error           text,
                                 revoked_notified_at  timestamptz,
                                 created_at           timestamptz NOT NULL DEFAULT now(),
                                 updated_at           timestamptz NOT NULL DEFAULT now(),
                                 CONSTRAINT google_accounts_user_key UNIQUE (user_id),
                                 CONSTRAINT google_accounts_identity_key UNIQUE (identity_id),
                                 CONSTRAINT google_accounts_id_user_key UNIQUE (id, user_id),
                                 CONSTRAINT google_accounts_status_check CHECK (status IN ('ok', 'revoked', 'error'))
);

CREATE TABLE sync_bindings (
                               id                 uuid PRIMARY KEY,
                               owner_id           uuid NOT NULL,
                               account_id         uuid NOT NULL,
                               entity             text NOT NULL,
                               local_calendar_id  uuid,
                               local_list_id      uuid,
                               remote_id          text NOT NULL,
                               remote_name        text NOT NULL DEFAULT '',
                               remote_access      text NOT NULL DEFAULT 'owner',
                               direction          text NOT NULL DEFAULT 'both',
                               enabled            boolean NOT NULL DEFAULT true,
                               sync_token         text,
                               updated_min        timestamptz,
                               watch_channel_id   text,
                               watch_resource_id  text,
                               watch_token_hash   bytea,
                               watch_expires_at   timestamptz,
                               next_poll_at       timestamptz NOT NULL DEFAULT now(),
                               last_synced_at     timestamptz,
                               last_error         text,
                               created_at         timestamptz NOT NULL DEFAULT now(),
                               updated_at         timestamptz NOT NULL DEFAULT now(),
                               CONSTRAINT sync_bindings_account_fkey FOREIGN KEY (account_id, owner_id)
                                   REFERENCES google_accounts (id, user_id) ON DELETE CASCADE,
                               CONSTRAINT sync_bindings_calendar_fkey FOREIGN KEY (local_calendar_id, owner_id)
                                   REFERENCES calendars (id, owner_id) ON DELETE CASCADE,
                               CONSTRAINT sync_bindings_list_fkey FOREIGN KEY (local_list_id, owner_id)
                                   REFERENCES todo_lists (id, owner_id) ON DELETE CASCADE,
                               CONSTRAINT sync_bindings_remote_key UNIQUE (account_id, entity, remote_id),
                               CONSTRAINT sync_bindings_calendar_key UNIQUE (local_calendar_id),
                               CONSTRAINT sync_bindings_list_key UNIQUE (local_list_id),
                               CONSTRAINT sync_bindings_watch_channel_key UNIQUE (watch_channel_id),
                               CONSTRAINT sync_bindings_entity_check CHECK (
                                   (entity = 'calendar' AND local_calendar_id IS NOT NULL AND local_list_id IS NULL)
                                       OR
                                   (entity = 'tasklist' AND local_list_id IS NOT NULL AND local_calendar_id IS NULL)
                                   ),
                               CONSTRAINT sync_bindings_access_check CHECK (
                                   remote_access IN ('owner', 'writer', 'reader', 'freeBusyReader')
                                   ),
                               CONSTRAINT sync_bindings_direction_check CHECK (direction IN ('both', 'pull', 'push')),
                               CONSTRAINT sync_bindings_watch_check CHECK (
                                   (watch_channel_id IS NULL) = (watch_resource_id IS NULL)
                                       AND (watch_channel_id IS NULL) = (watch_token_hash IS NULL)
                                       AND (watch_channel_id IS NULL) = (watch_expires_at IS NULL)
                                   )
);

CREATE INDEX sync_bindings_owner_idx ON sync_bindings (owner_id);
CREATE INDEX sync_bindings_poll_idx ON sync_bindings (next_poll_at) WHERE enabled;
CREATE INDEX sync_bindings_watch_expiry_idx ON sync_bindings (watch_expires_at)
    WHERE watch_channel_id IS NOT NULL;

CREATE TABLE sync_mappings (
                               binding_id             uuid NOT NULL REFERENCES sync_bindings (id) ON DELETE CASCADE,
                               entity                 text NOT NULL,
                               local_id               uuid NOT NULL,
                               remote_id              text NOT NULL,
                               remote_etag            text,
                               remote_updated         timestamptz,
                               local_version_at_sync  bigint NOT NULL DEFAULT 0,
                               remote_due_date        date,
                               synced_at              timestamptz NOT NULL DEFAULT now(),
                               PRIMARY KEY (binding_id, entity, local_id),
                               CONSTRAINT sync_mappings_remote_key UNIQUE (binding_id, entity, remote_id),
                               CONSTRAINT sync_mappings_entity_check CHECK (entity IN ('event', 'todo'))
);

CREATE INDEX sync_mappings_local_idx ON sync_mappings (entity, local_id);

CREATE TABLE sync_outbox (
                             id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
                             binding_id       uuid NOT NULL REFERENCES sync_bindings (id) ON DELETE CASCADE,
                             entity           text NOT NULL,
                             local_id         uuid NOT NULL,
                             op               text NOT NULL,
                             remote_id        text,
                             attempts         integer NOT NULL DEFAULT 0,
                             next_attempt_at  timestamptz NOT NULL DEFAULT now(),
                             last_error       text,
                             created_at       timestamptz NOT NULL DEFAULT now(),
                             updated_at       timestamptz NOT NULL DEFAULT now(),
                             CONSTRAINT sync_outbox_item_key UNIQUE (binding_id, entity, local_id),
                             CONSTRAINT sync_outbox_entity_check CHECK (entity IN ('event', 'todo')),
                             CONSTRAINT sync_outbox_op_check CHECK (op IN ('upsert', 'delete')),
                             CONSTRAINT sync_outbox_delete_check CHECK (op <> 'delete' OR remote_id IS NOT NULL)
);

CREATE INDEX sync_outbox_due_idx ON sync_outbox (next_attempt_at);

CREATE TABLE sync_conflicts (
                                id               uuid PRIMARY KEY,
                                owner_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                binding_id       uuid REFERENCES sync_bindings (id) ON DELETE SET NULL,
                                entity           text NOT NULL,
                                local_id         uuid NOT NULL,
                                remote_id        text NOT NULL,
                                winner           text NOT NULL,
                                local_snapshot   jsonb NOT NULL,
                                remote_snapshot  jsonb NOT NULL,
                                created_at       timestamptz NOT NULL DEFAULT now(),
                                CONSTRAINT sync_conflicts_entity_check CHECK (entity IN ('event', 'todo')),
                                CONSTRAINT sync_conflicts_winner_check CHECK (winner IN ('local', 'remote'))
);

CREATE INDEX sync_conflicts_owner_idx ON sync_conflicts (owner_id, created_at DESC);