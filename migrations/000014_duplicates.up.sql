CREATE TABLE duplicate_candidates (
                                      id           uuid PRIMARY KEY,
                                      owner_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                      entity       text NOT NULL,
                                      a_id         uuid NOT NULL,
                                      b_id         uuid NOT NULL,
                                      reason       text NOT NULL,
                                      score        real NOT NULL DEFAULT 1,
                                      status       text NOT NULL DEFAULT 'pending',
                                      created_at   timestamptz NOT NULL DEFAULT now(),
                                      resolved_at  timestamptz,
                                      CONSTRAINT duplicate_candidates_pair_key UNIQUE (owner_id, entity, a_id, b_id),
                                      CONSTRAINT duplicate_candidates_order_check CHECK (a_id < b_id),
                                      CONSTRAINT duplicate_candidates_entity_check CHECK (entity IN ('event', 'todo')),
                                      CONSTRAINT duplicate_candidates_reason_check CHECK (reason IN ('uid', 'fingerprint', 'fuzzy')),
                                      CONSTRAINT duplicate_candidates_score_check CHECK (score BETWEEN 0 AND 1),
                                      CONSTRAINT duplicate_candidates_status_check CHECK (
                                          status IN ('pending', 'merged', 'deleted', 'dismissed')
                                          ),
                                      CONSTRAINT duplicate_candidates_resolved_check CHECK ((status = 'pending') = (resolved_at IS NULL))
);

CREATE INDEX duplicate_candidates_owner_status_idx ON duplicate_candidates (owner_id, status, created_at DESC);
CREATE INDEX duplicate_candidates_a_idx ON duplicate_candidates (a_id);
CREATE INDEX duplicate_candidates_b_idx ON duplicate_candidates (b_id);

CREATE FUNCTION duplicate_candidates_cleanup() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    DELETE FROM duplicate_candidates
    WHERE entity = TG_ARGV[0] AND (a_id = OLD.id OR b_id = OLD.id);
    RETURN NULL;
END
$$;

CREATE TRIGGER events_duplicate_cleanup
    AFTER DELETE ON events
    FOR EACH ROW EXECUTE FUNCTION duplicate_candidates_cleanup('event');

CREATE TRIGGER todos_duplicate_cleanup
    AFTER DELETE ON todos
    FOR EACH ROW EXECUTE FUNCTION duplicate_candidates_cleanup('todo');