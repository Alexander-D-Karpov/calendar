-- name: CreateUser :one
INSERT INTO users (id, email, email_verified_at, password_hash, display_name, timezone)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at, id;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: SetUserDisabled :execrows
UPDATE users SET disabled_at = $2, updated_at = now() WHERE id = $1;