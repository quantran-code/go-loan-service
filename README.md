# go-loan-service

Records who borrowed which book, and when it is due back. Members and books are
owned by their own services.

```sh
MEMBERS_URL=http://localhost:8081 BOOKS_URL=http://localhost:8082 PORT=8083 go run .
```

`MEMBERS_URL` and `BOOKS_URL` are required. Before a loan is recorded the service
checks the member with the members service (`GET /members/{id}`) and asks the books
service to take the book out (`POST /books/{id}/borrow`).

| Route | Behaviour |
|---|---|
| `GET /health` | `{"status":"ok"}` when the process is up |
| `POST /loans` | Records a borrow: `201` with the loan, `400` for an invalid body, `404` for an unknown member or book, `409` when the book is already out, `502` when a dependency is unavailable |

## Borrowing a book

`POST /loans` names the book and the member. The member is checked first, then the
books service takes the book out, and only then is the loan recorded:

```json
{"book_id":1,"member_id":7}
```

```json
{
  "id": 1,
  "book_id": 1,
  "member_id": 7,
  "borrowed_at": "2024-05-01T12:30:00Z",
  "due_at": "2024-05-15T12:30:00Z"
}
```

`due_at` is `borrowed_at` plus 14 days, and the loan carries no status field: the due
date implies it.

## Request correlation

The API gateway mints an `X-Request-ID` and forwards it unchanged on every proxied
request. The loan service does not mint one of its own: it logs the id it received,
echoes it back on the response header, and makes it available to handlers through
`RequestIDFromContext`, so one value can be followed across the services:

```
request_id=<id> method=<method> path=<path>
```

A request that arrives without the header is still served and logged, with an empty
id.

Run the tests with `go test ./...`.
