-- name: InsertChange :one
INSERT INTO changes (owner_id, entity, entity_id, op, calendar_id, list_id, span, origin)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING seq;