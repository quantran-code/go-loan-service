-- +goose Up
CREATE TABLE loans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    member_id   INTEGER   NOT NULL,
    book_id     INTEGER   NOT NULL,
    borrowed_at TIMESTAMP NOT NULL,
    due_at      TIMESTAMP NOT NULL,
    returned_at TIMESTAMP
);

-- One active loan per member per book: the same person cannot borrow the same
-- title twice before returning it.
CREATE UNIQUE INDEX loans_active_idx ON loans (member_id, book_id) WHERE returned_at IS NULL;

-- +goose Down
DROP INDEX loans_active_idx;
DROP TABLE loans;
