-- Snapshot of the schema created by the runner used before Goose.
CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);

CREATE TABLE expenses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id > 0),
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    amount NUMERIC(14,2) NOT NULL CHECK (amount > 0 AND amount <= 999999999999.99),
    category TEXT NOT NULL CHECK (category IN ('еда', 'транспорт', 'жильё', 'развлечения', 'здоровье', 'покупки', 'другое')),
    spent_at DATE NOT NULL CHECK (spent_at BETWEEN DATE '0001-01-01' AND DATE '9999-12-31'),
    comment TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX expenses_user_date_idx ON expenses (user_id, spent_at DESC, id DESC);

INSERT INTO schema_migrations (version) VALUES ('001_expenses.sql');
