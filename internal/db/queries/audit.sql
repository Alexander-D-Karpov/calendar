-- name: InsertAudit :exec
INSERT INTO audit_log (user_id, action, ip, user_agent, meta)
VALUES ($1, $2, $3, $4, $5);