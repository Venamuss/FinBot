-- +goose Up
CREATE TABLE telegram_updates (
    update_id BIGINT PRIMARY KEY CHECK (update_id >= 0),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    response TEXT NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE telegram_updates;
