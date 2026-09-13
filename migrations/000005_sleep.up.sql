ALTER TABLE users ADD COLUMN sleep_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE sleep_schedule (
                                user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
                                weekday     smallint NOT NULL,
                                start_time  time NOT NULL,
                                end_time    time NOT NULL,
                                PRIMARY KEY (user_id, weekday),
                                CONSTRAINT sleep_schedule_weekday_check CHECK (weekday BETWEEN 0 AND 6),
                                CONSTRAINT sleep_schedule_window_check CHECK (start_time <> end_time)
);