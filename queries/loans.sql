-- name: CreateLoan :one
INSERT INTO loans (member_id, book_id, borrowed_at, due_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetLoan :one
SELECT * FROM loans WHERE id = ?;

-- name: ListLoansByMember :many
SELECT * FROM loans WHERE member_id = ? ORDER BY id;

-- name: FindActiveLoan :one
SELECT * FROM loans WHERE member_id = ? AND book_id = ? AND returned_at IS NULL;

-- name: MarkReturned :execrows
UPDATE loans SET returned_at = ? WHERE id = ? AND returned_at IS NULL;
