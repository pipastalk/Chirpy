-- name: ResetUserDB :exec
Delete FROM users WHERE id IS NOT NULL;