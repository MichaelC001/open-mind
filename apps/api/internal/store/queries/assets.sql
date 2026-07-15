-- name: CreateAsset :one
INSERT INTO assets (user_id, item_id, content_type, byte_size, original_filename)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetAsset :one
SELECT * FROM assets WHERE user_id = $1 AND id = $2;

-- name: SetAssetByteSize :exec
UPDATE assets SET byte_size = $3 WHERE user_id = $1 AND id = $2;

-- name: GetAssetByItem :one
SELECT * FROM assets WHERE user_id = $1 AND item_id = $2;

-- name: DeleteAsset :exec
DELETE FROM assets WHERE user_id = $1 AND id = $2;
