-- name: CreateDefaultTodoList :exec
INSERT INTO todo_lists (id, owner_id, name, is_default)
VALUES ($1, $2, $3, true);