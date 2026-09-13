ALTER TABLE duplicate_candidates ADD COLUMN a_snapshot jsonb;
ALTER TABLE duplicate_candidates ADD COLUMN b_snapshot jsonb;

CREATE INDEX events_owner_trash_idx ON events (owner_id, deleted_at DESC)
    WHERE deleted_at IS NOT NULL AND series_id IS NULL;
CREATE INDEX todos_owner_trash_idx ON todos (owner_id, deleted_at DESC)
    WHERE deleted_at IS NOT NULL AND parent_id IS NULL;