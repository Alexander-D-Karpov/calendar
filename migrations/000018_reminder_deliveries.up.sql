CREATE TABLE reminder_deliveries (
                                     entity          text NOT NULL,
                                     entity_id       uuid NOT NULL,
                                     occurrence_at   timestamptz NOT NULL,
                                     minutes_before  integer NOT NULL,
                                     owner_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                     fire_at         timestamptz NOT NULL,
                                     push_sent       integer NOT NULL DEFAULT 0,
                                     delivered_at    timestamptz NOT NULL DEFAULT now(),
                                     PRIMARY KEY (entity, entity_id, occurrence_at, minutes_before),
                                     CONSTRAINT reminder_deliveries_entity_check CHECK (entity IN ('event', 'todo')),
                                     CONSTRAINT reminder_deliveries_minutes_check CHECK (minutes_before BETWEEN 0 AND 40320),
                                     CONSTRAINT reminder_deliveries_push_check CHECK (push_sent >= 0)
);

CREATE INDEX reminder_deliveries_owner_idx ON reminder_deliveries (owner_id, delivered_at DESC);
CREATE INDEX reminder_deliveries_delivered_at_idx ON reminder_deliveries (delivered_at);