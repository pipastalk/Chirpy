-- name: UpdateUserAuthentication :one
UPDATE users
SET email = $2, password = $3
where id = $1
RETURNING *;