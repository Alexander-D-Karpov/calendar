ALTER TABLE users ADD COLUMN secondary_timezone text;
ALTER TABLE users ADD COLUMN work_end time NOT NULL DEFAULT '18:00';
ALTER TABLE users ADD COLUMN work_start time NOT NULL DEFAULT '09:00';
ALTER TABLE users ADD COLUMN slot_minutes smallint NOT NULL DEFAULT 30;
ALTER TABLE users ADD COLUMN theme text NOT NULL DEFAULT 'system';
ALTER TABLE users ADD CONSTRAINT users_slot_minutes_check CHECK (slot_minutes IN (10, 15, 20, 30, 60));
ALTER TABLE users ADD CONSTRAINT users_theme_check CHECK (theme IN ('system', 'light', 'dark'));
ALTER TABLE users ADD CONSTRAINT users_work_hours_check CHECK (work_start < work_end);
