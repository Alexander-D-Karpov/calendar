DROP INDEX IF EXISTS todos_owner_trash_idx;
DROP INDEX IF EXISTS events_owner_trash_idx;
ALTER TABLE duplicate_candidates DROP COLUMN IF EXISTS b_snapshot;
ALTER TABLE duplicate_candidates DROP COLUMN IF EXISTS a_snapshot;