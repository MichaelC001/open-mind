-- name: GetUserSetting :one
SELECT value FROM user_settings WHERE user_id = $1 AND key = $2;

-- name: UpsertUserSetting :exec
INSERT INTO user_settings (user_id, key, value) VALUES ($1, $2, $3)
ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: DeleteUserSetting :execrows
DELETE FROM user_settings WHERE user_id = $1 AND key = $2;

-- name: ListUserSettings :many
SELECT key, value FROM user_settings WHERE user_id = $1;
