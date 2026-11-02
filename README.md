# go-loan-service

Records who borrowed which book, and when it is due back. Members and books are
owned by their own services.

```sh
PORT=8083 go run .
```

| Route | Behaviour |
|---|---|
| `GET /health` | `{"status":"ok"}` when the process is up |

Run the tests with `go test ./...`.
