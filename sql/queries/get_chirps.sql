-- name: GetChirps :many
SELECT * FROM posts
ORDER BY created_at ASC;