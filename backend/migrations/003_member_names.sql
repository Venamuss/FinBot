-- +goose Up
-- Display metadata only: expense ownership does not depend on this table.
CREATE TABLE member_names (
    user_id BIGINT PRIMARY KEY CHECK (user_id > 0),
    display_name TEXT CHECK (
        display_name IS NULL OR (
            char_length(display_name) BETWEEN 1 AND 100
            AND display_name = btrim(display_name)
        )
    )
);

-- +goose Down
DROP TABLE member_names;
