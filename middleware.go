package main

import (
	"context"
	"log"
	"net/http"
)

// RequestIDHeader is the wire header carrying the correlation ID minted by the gateway.
const RequestIDHeader = "X-Request-ID"

// requestIDKey is the request context key under which the request ID is stored.
type requestIDKey struct{}

// RequestIDMiddleware stores the request ID forwarded by the gateway on the request
// context, echoes it back on the response and logs one line per request.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id != "" {
			w.Header().Set(RequestIDHeader, id)
		}
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
		log.Printf("request_id=%s method=%s path=%s", id, r.Method, r.URL.Path)
	})
}

// RequestIDFromContext returns the request ID stored by RequestIDMiddleware, or "" when
// the middleware did not run.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
