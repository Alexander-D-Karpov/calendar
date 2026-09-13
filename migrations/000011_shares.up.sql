CREATE TABLE share_links (
                             id                uuid PRIMARY KEY,
                             owner_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                             name              text NOT NULL,
                             token_hash        bytea NOT NULL,
                             token_enc         bytea NOT NULL,
                             view              text NOT NULL,
                             period_date       date NOT NULL,
                             detail            text NOT NULL DEFAULT 'titles',
                             include_todos     boolean NOT NULL DEFAULT false,
                             tz_mode           text NOT NULL DEFAULT 'owner',
                             version           integer NOT NULL DEFAULT 1,
                             access_count      bigint NOT NULL DEFAULT 0,
                             last_accessed_at  timestamptz,
                             created_at        timestamptz NOT NULL DEFAULT now(),
                             updated_at        timestamptz NOT NULL DEFAULT now(),
                             revoked_at        timestamptz,
                             CONSTRAINT share_links_id_owner_key UNIQUE (id, owner_id),
                             CONSTRAINT share_links_token_hash_key UNIQUE (token_hash),
                             CONSTRAINT share_links_name_check CHECK (char_length(name) BETWEEN 1 AND 100),
                             CONSTRAINT share_links_view_check CHECK (view IN ('day', '3day', 'week', 'month', 'year', 'agenda')),
                             CONSTRAINT share_links_detail_check CHECK (detail IN ('busy', 'titles', 'full')),
                             CONSTRAINT share_links_tz_mode_check CHECK (tz_mode IN ('owner', 'viewer'))
);

CREATE INDEX share_links_owner_idx ON share_links (owner_id, created_at DESC);

CREATE TABLE share_link_calendars (
                                      share_id     uuid NOT NULL,
                                      calendar_id  uuid NOT NULL,
                                      owner_id     uuid NOT NULL,
                                      PRIMARY KEY (share_id, calendar_id),
                                      CONSTRAINT share_link_calendars_share_fkey FOREIGN KEY (share_id, owner_id)
                                          REFERENCES share_links (id, owner_id) ON DELETE CASCADE,
                                      CONSTRAINT share_link_calendars_calendar_fkey FOREIGN KEY (calendar_id, owner_id)
                                          REFERENCES calendars (id, owner_id) ON DELETE CASCADE
);

CREATE INDEX share_link_calendars_calendar_idx ON share_link_calendars (calendar_id);