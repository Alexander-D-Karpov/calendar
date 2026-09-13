CREATE SEQUENCE changes_seq AS bigint;

CREATE TABLE changes (
                         seq          bigint PRIMARY KEY,
                         owner_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                         entity       text NOT NULL,
                         entity_id    uuid NOT NULL,
                         op           text NOT NULL,
                         calendar_id  uuid,
                         list_id      uuid,
                         span         tstzrange,
                         origin       text,
                         created_at   timestamptz NOT NULL DEFAULT now(),
                         CONSTRAINT changes_entity_check CHECK (
                             entity IN ('event', 'todo', 'calendar', 'list', 'share', 'subscription', 'import', 'duplicate', 'settings', 'sync')
                             ),
                         CONSTRAINT changes_op_check CHECK (op IN ('create', 'update', 'delete', 'restore')),
                         CONSTRAINT changes_origin_check CHECK (origin IS NULL OR char_length(origin) <= 64)
);

ALTER SEQUENCE changes_seq OWNED BY changes.seq;

CREATE INDEX changes_owner_seq_idx ON changes (owner_id, seq);
CREATE INDEX changes_created_at_idx ON changes (created_at);

CREATE FUNCTION changes_assign_seq() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(7261002, hashtext(NEW.owner_id::text));
    NEW.seq := nextval('changes_seq');
    RETURN NEW;
END
$$;

CREATE TRIGGER changes_assign_seq
    BEFORE INSERT ON changes
    FOR EACH ROW EXECUTE FUNCTION changes_assign_seq();

CREATE FUNCTION changes_notify() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    PERFORM pg_notify('calendar_changes', json_build_object(
            'seq', NEW.seq,
            'owner', NEW.owner_id,
            'entity', NEW.entity,
            'id', NEW.entity_id,
            'op', NEW.op,
            'calendar', NEW.calendar_id,
            'list', NEW.list_id,
            'from', lower(NEW.span),
            'to', upper(NEW.span),
            'origin', NEW.origin
                                          )::text);
    RETURN NULL;
END
$$;

CREATE TRIGGER changes_notify
    AFTER INSERT ON changes
    FOR EACH ROW EXECUTE FUNCTION changes_notify();