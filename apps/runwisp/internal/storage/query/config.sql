-- name: GetConfigValue :one
SELECT value FROM config_entries WHERE key = ?;

-- name: SetConfigValue :exec
INSERT INTO config_entries (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;
