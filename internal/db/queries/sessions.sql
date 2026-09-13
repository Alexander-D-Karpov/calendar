-- name: CreateSession :exec
INSERT INTO sessions (id, token_hash, user_id, csrf_secret, ip, user_agent, created_at, last_seen_at, expires_at, absolute_expires_at, reauth_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetSessionByTokenHash :one
SELECT s.* FROM sessions s
                    JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND u.disabled_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE id = $1;

-- name: SetSessionReauth :exec
UPDATE sessions SET reauth_at = $2 WHERE id = $1;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE id = $1 AND user_id = $2;

-- name: DeleteUserSessionsExcept :execrows
DELETE FROM sessions WHERE user_id = $1 AND id <> $2;

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = $1 ORDER BY last_seen_at DESC;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < @now OR absolute_expires_at < @now;