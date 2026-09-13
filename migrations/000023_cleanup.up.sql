ALTER TABLE users DROP CONSTRAINT users_work_hours_check;
ALTER TABLE users DROP CONSTRAINT users_theme_check;
ALTER TABLE users DROP CONSTRAINT users_slot_minutes_check;
ALTER TABLE users DROP COLUMN theme;
ALTER TABLE users DROP COLUMN slot_minutes;
ALTER TABLE users DROP COLUMN work_start;
ALTER TABLE users DROP COLUMN work_end;
ALTER TABLE users DROP COLUMN secondary_timezone;
