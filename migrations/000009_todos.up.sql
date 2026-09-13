CREATE TABLE todo_lists (
                            id          uuid PRIMARY KEY,
                            owner_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                            name        text NOT NULL,
                            color       text NOT NULL DEFAULT '#5b7c5a',
                            position    integer NOT NULL DEFAULT 0,
                            is_default  boolean NOT NULL DEFAULT false,
                            created_at  timestamptz NOT NULL DEFAULT now(),
                            updated_at  timestamptz NOT NULL DEFAULT now(),
                            CONSTRAINT todo_lists_id_owner_key UNIQUE (id, owner_id),
                            CONSTRAINT todo_lists_name_check CHECK (char_length(name) BETWEEN 1 AND 100),
                            CONSTRAINT todo_lists_color_check CHECK (color ~ '^#[0-9a-f]{6}$')
);

CREATE UNIQUE INDEX todo_lists_owner_default_idx ON todo_lists (owner_id) WHERE is_default;
CREATE INDEX todo_lists_owner_position_idx ON todo_lists (owner_id, position);

CREATE TABLE todos (
                       id                uuid PRIMARY KEY,
                       owner_id          uuid NOT NULL,
                       list_id           uuid NOT NULL,
                       parent_id         uuid,
                       ical_uid          text,
                       title             text NOT NULL DEFAULT '',
                       body              text NOT NULL DEFAULT '',
                       status            text NOT NULL DEFAULT 'needs_action',
                       priority          smallint NOT NULL DEFAULT 0,
                       position          text COLLATE "C" NOT NULL,
                       due_date          date,
                       due_time          time,
                       duration_min      integer,
                       tz                text,
                       rrule             text,
                       show_on_calendar  boolean NOT NULL DEFAULT false,
                       completed_at      timestamptz,
                       version           bigint NOT NULL DEFAULT 1,
                       fingerprint       bytea,
                       source_hash       bytea,
                       created_at        timestamptz NOT NULL DEFAULT now(),
                       updated_at        timestamptz NOT NULL DEFAULT now(),
                       deleted_at        timestamptz,
                       CONSTRAINT todos_id_list_key UNIQUE (id, list_id),
                       CONSTRAINT todos_list_fkey FOREIGN KEY (list_id, owner_id)
                           REFERENCES todo_lists (id, owner_id) ON DELETE CASCADE,
                       CONSTRAINT todos_parent_fkey FOREIGN KEY (parent_id, list_id)
                           REFERENCES todos (id, list_id) ON DELETE CASCADE ON UPDATE CASCADE,
                       CONSTRAINT todos_parent_self_check CHECK (parent_id IS NULL OR parent_id <> id),
                       CONSTRAINT todos_uid_check CHECK (ical_uid IS NULL OR char_length(ical_uid) BETWEEN 1 AND 1000),
                       CONSTRAINT todos_title_check CHECK (char_length(title) <= 1000),
                       CONSTRAINT todos_body_check CHECK (char_length(body) <= 100000),
                       CONSTRAINT todos_status_check CHECK (status IN ('needs_action', 'completed')),
                       CONSTRAINT todos_completed_check CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
                       CONSTRAINT todos_priority_check CHECK (priority BETWEEN 0 AND 3),
                       CONSTRAINT todos_position_check CHECK (char_length(position) BETWEEN 1 AND 128),
                       CONSTRAINT todos_due_time_check CHECK (due_time IS NULL OR due_date IS NOT NULL),
                       CONSTRAINT todos_tz_check CHECK (due_time IS NULL OR tz IS NOT NULL),
                       CONSTRAINT todos_duration_check CHECK (
                           duration_min IS NULL OR (due_time IS NOT NULL AND duration_min BETWEEN 1 AND 1440)
                           ),
                       CONSTRAINT todos_rrule_check CHECK (
                           rrule IS NULL OR (due_date IS NOT NULL AND char_length(rrule) BETWEEN 1 AND 1000)
                           )
);

CREATE INDEX todos_list_id_idx ON todos (list_id);
CREATE INDEX todos_list_order_idx ON todos (list_id, parent_id, position) WHERE deleted_at IS NULL;
CREATE INDEX todos_parent_idx ON todos (parent_id, list_id) WHERE parent_id IS NOT NULL;
CREATE INDEX todos_owner_due_idx ON todos (owner_id, due_date)
    WHERE deleted_at IS NULL AND due_date IS NOT NULL;
CREATE INDEX todos_owner_status_idx ON todos (owner_id, status, completed_at) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX todos_list_uid_idx ON todos (list_id, ical_uid)
    WHERE ical_uid IS NOT NULL AND parent_id IS NULL AND deleted_at IS NULL;
CREATE INDEX todos_owner_fingerprint_idx ON todos (owner_id, fingerprint)
    WHERE fingerprint IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX todos_owner_deleted_idx ON todos (owner_id, deleted_at) WHERE deleted_at IS NOT NULL;

CREATE FUNCTION todos_check_depth() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    IF NEW.parent_id IS NULL OR NEW.deleted_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    PERFORM pg_advisory_xact_lock(7261003, hashtext(NEW.list_id::text));
    IF EXISTS (SELECT 1 FROM todos WHERE id = NEW.parent_id AND parent_id IS NOT NULL) THEN
        RAISE EXCEPTION 'subtasks cannot have subtasks'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'todos_depth_check';
    END IF;
    IF EXISTS (SELECT 1 FROM todos WHERE parent_id = NEW.id AND deleted_at IS NULL) THEN
        RAISE EXCEPTION 'a todo with subtasks cannot become a subtask'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'todos_depth_check';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER todos_check_depth
    BEFORE INSERT OR UPDATE OF parent_id, deleted_at ON todos
    FOR EACH ROW EXECUTE FUNCTION todos_check_depth();

CREATE TABLE todo_checks (
                             id          uuid PRIMARY KEY,
                             todo_id     uuid NOT NULL REFERENCES todos (id) ON DELETE CASCADE,
                             text        text NOT NULL,
                             done        boolean NOT NULL DEFAULT false,
                             position    text COLLATE "C" NOT NULL,
                             done_at     timestamptz,
                             created_at  timestamptz NOT NULL DEFAULT now(),
                             CONSTRAINT todo_checks_text_check CHECK (char_length(text) BETWEEN 1 AND 500),
                             CONSTRAINT todo_checks_done_check CHECK (done = (done_at IS NOT NULL)),
                             CONSTRAINT todo_checks_position_check CHECK (char_length(position) BETWEEN 1 AND 128)
);

CREATE INDEX todo_checks_todo_position_idx ON todo_checks (todo_id, position);

CREATE TABLE todo_reminders (
                                todo_id         uuid NOT NULL REFERENCES todos (id) ON DELETE CASCADE,
                                minutes_before  integer NOT NULL,
                                PRIMARY KEY (todo_id, minutes_before),
                                CONSTRAINT todo_reminders_minutes_check CHECK (minutes_before BETWEEN 0 AND 40320)
);