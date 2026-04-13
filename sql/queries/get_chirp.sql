-- name: GetChirp :one
SELECT * FROM posts
where id = $1;