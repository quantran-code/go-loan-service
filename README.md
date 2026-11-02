# go-loan-service

Records who borrowed which book and when it is due back. SQLite with `sqlc` and
`goose`; members and books are resolved through their own services.

```sh
DB_PATH=loans.db PORT=8083 LOAN_DAYS=14 \
MEMBER_SERVICE_URL=http://localhost:8081 \
CATALOG_SERVICE_URL=http://localhost:8082 \
go run .
```

| Route | Behaviour |
|---|---|
| `GET /health` | liveness |
| `POST /loans` | `{member_id, book_id}`; reserves a copy, `404` for an unknown member or book, `409` when no copy is free or the member already has it |
| `POST /loans/{id}/return` | returns the copy; `409` when it was already returned |
| `GET /loans?member_id=` | that member's loans, oldest first |

A rejected borrow never leaves a copy off the shelf: the reservation is undone when
the loan row cannot be written.

Regenerate the query layer with `go generate ./...` (sqlc); never edit `dbgen` by
hand. Run the tests with `go test ./...`.
