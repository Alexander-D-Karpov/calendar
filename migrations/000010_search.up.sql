CREATE FUNCTION search_document(primary_text text, secondary_text text, tertiary_text text) RETURNS tsvector
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    SET search_path FROM CURRENT
AS $$
SELECT
    setweight(to_tsvector('english'::regconfig, f_unaccent(coalesce(primary_text, ''))), 'A') ||
    setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(primary_text, ''))), 'A') ||
    setweight(to_tsvector('english'::regconfig, f_unaccent(coalesce(secondary_text, ''))), 'B') ||
    setweight(to_tsvector('simple'::regconfig, f_unaccent(coalesce(secondary_text, ''))), 'B') ||
    setweight(to_tsvector('english'::regconfig, f_unaccent(left(coalesce(tertiary_text, ''), 20000))), 'C') ||
    setweight(to_tsvector('simple'::regconfig, f_unaccent(left(coalesce(tertiary_text, ''), 20000))), 'C')
$$;

ALTER TABLE events
    ADD COLUMN search tsvector GENERATED ALWAYS AS (search_document(title, location, body)) STORED;

CREATE INDEX events_search_idx ON events USING gin (search) WHERE deleted_at IS NULL;
CREATE INDEX events_title_trgm_idx ON events USING gin (lower(f_unaccent(title)) gin_trgm_ops)
    WHERE deleted_at IS NULL;

ALTER TABLE todos ADD COLUMN search tsvector NOT NULL DEFAULT ''::tsvector;

CREATE FUNCTION todo_checks_text(p_todo_id uuid) RETURNS text
    LANGUAGE sql
    STABLE
    SET search_path FROM CURRENT
AS $$
SELECT coalesce(string_agg(c.text, ' ' ORDER BY c.position), '')
FROM todo_checks c
WHERE c.todo_id = p_todo_id
$$;

CREATE FUNCTION todos_search_update() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    NEW.search := search_document(NEW.title, todo_checks_text(NEW.id), NEW.body);
    RETURN NEW;
END
$$;

CREATE TRIGGER todos_search_update
    BEFORE INSERT OR UPDATE OF title, body ON todos
    FOR EACH ROW EXECUTE FUNCTION todos_search_update();

CREATE FUNCTION todo_checks_search_refresh() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        UPDATE todos t
        SET search = search_document(t.title, todo_checks_text(t.id), t.body)
        WHERE t.id = OLD.todo_id;
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.todo_id IS DISTINCT FROM OLD.todo_id) THEN
        UPDATE todos t
        SET search = search_document(t.title, todo_checks_text(t.id), t.body)
        WHERE t.id = NEW.todo_id;
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER todo_checks_search_refresh
    AFTER INSERT OR DELETE OR UPDATE OF text, position, todo_id ON todo_checks
    FOR EACH ROW EXECUTE FUNCTION todo_checks_search_refresh();

UPDATE todos t SET search = search_document(t.title, todo_checks_text(t.id), t.body);

CREATE INDEX todos_search_idx ON todos USING gin (search) WHERE deleted_at IS NULL;
CREATE INDEX todos_title_trgm_idx ON todos USING gin (lower(f_unaccent(title)) gin_trgm_ops)
    WHERE deleted_at IS NULL;