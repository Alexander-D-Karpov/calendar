-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, user_id, name, prefix, secret_hash, scopes, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetAPITokenByPrefix :one
SELECT t.* FROM api_tokens t
                    JOIN users u ON u.id = t.user_id
WHERE t.prefix = $1 AND u.disabled_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = $2, last_used_ip = $3 WHERE id = $1;

-- name: ListUserAPITokens :many
SELECT * FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC;

-- name: RevokeAPIToken :execrows
UPDATE api_tokens SET revoked_at = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;