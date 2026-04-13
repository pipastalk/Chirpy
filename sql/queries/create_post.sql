-- name: CreatePost :one
INSERT INTO posts (id, created_at, updated_at, body, user_id)
VALUES (
    DEFAULT, DEFAULT, DEFAULT, $1, $2
)
RETURNING *;