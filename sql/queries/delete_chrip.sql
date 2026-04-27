-- name: DeleteChirp :exec
DELETE FROM posts
where id = $1;