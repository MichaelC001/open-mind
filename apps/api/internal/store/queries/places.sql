-- name: DeleteItemPlaces :exec
DELETE FROM item_places WHERE user_id = $1 AND item_id = $2;

-- name: InsertItemPlace :exec
INSERT INTO item_places (user_id, item_id, name, hint, address, lat, lng, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListItemPlaces :many
SELECT * FROM item_places WHERE user_id = $1 AND item_id = $2 ORDER BY created_at, name;
