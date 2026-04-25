-- name: CreateUser :one
INSERT INTO users (id, created_at, updated_at, email, password)
VALUES (
    DEFAULT, DEFAULT, DEFAULT, $1, $2
)
RETURNING *;