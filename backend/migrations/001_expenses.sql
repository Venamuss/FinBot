-- +goose Up
-- IF NOT EXISTS preserves databases initialized by the original migration runner.
CREATE TABLE IF NOT EXISTS expenses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id > 0),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    amount NUMERIC(14,2) NOT NULL CHECK (amount > 0 AND amount <= 999999999999.99),
    category TEXT NOT NULL CHECK (category IN ('еда', 'транспорт', 'жильё', 'развлечения', 'здоровье', 'покупки', 'другое')),
    spent_at DATE NOT NULL CHECK (spent_at BETWEEN DATE '0001-01-01' AND DATE '9999-12-31'),
    comment TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS expenses_user_date_idx ON expenses (user_id, spent_at DESC, id DESC);

-- +goose Down
DROP TABLE expenses;
