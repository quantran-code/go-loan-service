# go-loan-service

Records who borrowed which book, and when it is due back. Members and books are
owned by their own services.

```sh
PORT=8083 go run .
```

| Route | Behaviour |
|---|---|
| `GET /health` | `{"status":"ok"}` when the process is up |

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
