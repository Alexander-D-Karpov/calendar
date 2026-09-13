CREATE TABLE jobs (
                      id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
                      kind          text NOT NULL,
                      payload       jsonb NOT NULL DEFAULT '{}'::jsonb,
                      unique_key    text,
                      status        text NOT NULL DEFAULT 'queued',
                      priority      smallint NOT NULL DEFAULT 0,
                      run_at        timestamptz NOT NULL DEFAULT now(),
                      attempts      integer NOT NULL DEFAULT 0,
                      max_attempts  integer NOT NULL DEFAULT 10,
                      locked_by     text,
                      locked_at     timestamptz,
                      last_error    text,
                      created_at    timestamptz NOT NULL DEFAULT now(),
                      updated_at    timestamptz NOT NULL DEFAULT now(),
                      CONSTRAINT jobs_kind_check CHECK (char_length(kind) BETWEEN 1 AND 64),
                      CONSTRAINT jobs_unique_key_check CHECK (unique_key IS NULL OR char_length(unique_key) BETWEEN 1 AND 255),
                      CONSTRAINT jobs_status_check CHECK (status IN ('queued', 'running', 'failed')),
                      CONSTRAINT jobs_attempts_check CHECK (attempts >= 0 AND max_attempts >= 1),
                      CONSTRAINT jobs_lock_check CHECK ((status = 'running') = (locked_at IS NOT NULL))
);

CREATE UNIQUE INDEX jobs_unique_key_idx ON jobs (kind, unique_key)
    WHERE unique_key IS NOT NULL AND status = 'queued';
CREATE INDEX jobs_ready_idx ON jobs (priority DESC, run_at) WHERE status = 'queued';
CREATE INDEX jobs_running_idx ON jobs (locked_at) WHERE status = 'running';
CREATE INDEX jobs_failed_idx ON jobs (updated_at) WHERE status = 'failed';

CREATE FUNCTION jobs_notify() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path FROM CURRENT
AS $$
BEGIN
    PERFORM pg_notify('calendar_jobs', NEW.kind);
    RETURN NULL;
END
$$;

CREATE TRIGGER jobs_notify
    AFTER INSERT ON jobs
    FOR EACH ROW
    WHEN (NEW.status = 'queued')
EXECUTE FUNCTION jobs_notify();