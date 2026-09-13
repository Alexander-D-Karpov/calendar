-- name: CreateDefaultCalendar :exec
INSERT INTO calendars (id, owner_id, name, is_default)
VALUES ($1, $2, $3, true);

-- name: ListCalendars :many
SELECT * FROM calendars WHERE owner_id = $1 ORDER BY position, created_at, id;

-- name: CountCalendars :one
SELECT count(*) FROM calendars WHERE owner_id = $1;

-- name: GetCalendar :one
SELECT * FROM calendars WHERE id = $1 AND owner_id = $2;

-- name: GetCalendarForUpdate :one
SELECT * FROM calendars WHERE id = $1 AND owner_id = $2 FOR UPDATE;

-- name: NextCalendarPosition :one
SELECT (coalesce(max(position), -1) + 1)::int AS next FROM calendars WHERE owner_id = $1;

-- name: CreateCalendar :one
INSERT INTO calendars (id, owner_id, name, color, description, timezone, kind, read_only, hidden, position, default_reminders)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateCalendar :one
UPDATE calendars
SET name = $3, color = $4, description = $5, timezone = $6, hidden = $7, position = $8, default_reminders = $9, updated_at = now()
WHERE id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteCalendar :execrows
DELETE FROM calendars WHERE id = $1 AND owner_id = $2 AND NOT is_default;