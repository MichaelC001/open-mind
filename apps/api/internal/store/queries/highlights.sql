-- name: CreateHighlight :one
INSERT INTO highlights (user_id, source_item_id, quote_item_id, exact, prefix, suffix, offset_hint)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: ListHighlightsBySource :many
SELECT * FROM highlights WHERE user_id = $1 AND source_item_id = $2 ORDER BY created_at ASC;

-- name: GetHighlight :one
SELECT * FROM highlights WHERE user_id = $1 AND id = $2;

-- name: DeleteHighlight :execrows
DELETE FROM highlights WHERE user_id = $1 AND id = $2;
