package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// captureLog redirects the standard logger into a buffer for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buf
}

func TestRequestsAreLoggedWithTheRequestID(t *testing.T) {
	buf := captureLog(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(RequestIDHeader, "abc")
	rec := httptest.NewRecorder()

	NewRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get(RequestIDHeader); got != "abc" {
		t.Fatalf("response %s = %q, want %q", RequestIDHeader, got, "abc")
	}

	out := buf.String()
	for _, want := range []string{"request_id=abc", "method=GET", "path=/health"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q: %s", want, out)
		}
	}
}

func TestRequestsWithoutRequestIDAreStillServedAndLogged(t *testing.T) {
	buf := captureLog(t)

	rec := httptest.NewRecorder()
	NewRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get(RequestIDHeader); got != "" {
		t.Fatalf("response %s = %q, want no header", RequestIDHeader, got)
	}
	if out := buf.String(); !strings.Contains(out, "method=GET") || !strings.Contains(out, "path=/health") {
		t.Fatalf("log missing request line: %s", out)
	}
}
