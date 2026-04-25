-- name: GetChirpByUser :many
SELECT * FROM posts
WHERE user_id = $1
ORDER BY created_at ASC;