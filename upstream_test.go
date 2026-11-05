package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// upstreamTimeout is short so a test against an unreachable service cannot hang.
const upstreamTimeout = time.Second

// baseURL parses an httptest server URL into the *url.URL the clients take.
func baseURL(t *testing.T, server *httptest.Server) *url.URL {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing server URL %s: %v", server.URL, err)
	}
	return base
}

func TestMembersClientMapsStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		serve  bool
		want   error
	}{
		{name: "200", status: http.StatusOK, serve: true, want: nil},
		{name: "404", status: http.StatusNotFound, serve: true, want: errMemberNotFound},
		{name: "500", status: http.StatusInternalServerError, serve: true, want: errUpstreamUnavailable},
		{name: "unreachable", serve: false, want: errUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want %s", r.Method, http.MethodGet)
				}
				if r.URL.Path != "/members/7" {
					t.Errorf("path = %s, want /members/7", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			base := baseURL(t, server)
			if tc.serve {
				defer server.Close()
			} else {
				server.Close()
			}

			client := newMembersClient(base, upstreamTimeout)
			if got := client.memberExists(context.Background(), 7); got != tc.want {
				t.Fatalf("memberExists() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBooksClientMapsStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		serve  bool
		want   error
	}{
		{name: "200", status: http.StatusOK, serve: true, want: nil},
		{name: "404", status: http.StatusNotFound, serve: true, want: errBookNotFound},
		{name: "409", status: http.StatusConflict, serve: true, want: errBookOnLoan},
		{name: "500", status: http.StatusInternalServerError, serve: true, want: errUpstreamUnavailable},
		{name: "unreachable", serve: false, want: errUpstreamUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want %s", r.Method, http.MethodPost)
				}
				if r.URL.Path != "/books/3/borrow" {
					t.Errorf("path = %s, want /books/3/borrow", r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			base := baseURL(t, server)
			if tc.serve {
				defer server.Close()
			} else {
				server.Close()
			}

			client := newBooksClient(base, upstreamTimeout)
			if got := client.borrow(context.Background(), 3); got != tc.want {
				t.Fatalf("borrow() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClientsForwardTheRequestID(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get(RequestIDHeader))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	base := baseURL(t, server)
	ctx := context.WithValue(context.Background(), requestIDKey{}, "abc")

	if err := newMembersClient(base, upstreamTimeout).memberExists(ctx, 7); err != nil {
		t.Fatalf("memberExists() = %v, want nil", err)
	}
	if err := newBooksClient(base, upstreamTimeout).borrow(ctx, 3); err != nil {
		t.Fatalf("borrow() = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(seen))
	}
	for i, id := range seen {
		if id != "abc" {
			t.Fatalf("request %d carried %s = %q, want %q", i, RequestIDHeader, id, "abc")
		}
	}
}
