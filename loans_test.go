package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// upstreamRequest is one request a fake dependency served.
type upstreamRequest struct {
	method    string
	path      string
	requestID string
}

// fakeUpstream stands in for the members or books service: it records every request
// it serves and answers with the status the test picked.
type fakeUpstream struct {
	mu       sync.Mutex
	requests []upstreamRequest
	status   int
	server   *httptest.Server
}

// newFakeUpstream starts a fake dependency answering every request with status.
func newFakeUpstream(t *testing.T, status int) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{status: status}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, upstreamRequest{
			method:    r.Method,
			path:      r.URL.Path,
			requestID: r.Header.Get(RequestIDHeader),
		})
		status := f.status
		f.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// served returns a copy of the requests the fake has answered so far.
func (f *fakeUpstream) served() []upstreamRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamRequest(nil), f.requests...)
}

// newLoanTestRouter serves POST /loans over the two dependency bases through the same
// request-ID middleware the service uses, and hands back the API so a test can read
// the store the loan is recorded in.
func newLoanTestRouter(membersBase, booksBase *url.URL) (http.Handler, *loanAPI) {
	loans := newLoanStore()
	api := newLoanAPI(loans,
		newMembersClient(membersBase, upstreamTimeout),
		newBooksClient(booksBase, upstreamTimeout))
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware)
	r.Post("/loans", api.createLoan)
	return r, api
}

// postLoan sends body to POST /loans and returns the recorded response.
func postLoan(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/loans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// storedLoans copies the loans the store currently holds.
func storedLoans(store *loanStore) []Loan {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]Loan(nil), store.loans...)
}

// decodeLoan decodes a loan response body.
func decodeLoan(t *testing.T, rec *httptest.ResponseRecorder) Loan {
	t.Helper()
	var loan Loan
	if err := json.Unmarshal(rec.Body.Bytes(), &loan); err != nil {
		t.Fatalf("decoding loan from %s: %v", rec.Body, err)
	}
	return loan
}

// assertErrorResponse checks the status and that the body is {"error": want}.
func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body from %s: %v", rec.Body, err)
	}
	if body.Error != want {
		t.Fatalf("error = %q, want %q", body.Error, want)
	}
}

func TestCreateLoanRecordsTheBorrow(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusOK)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	borrowedAt := time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC)
	api.now = func() time.Time { return borrowedAt }

	rec := postLoan(t, router, `{"book_id":1,"member_id":7}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body)
	}

	created := decodeLoan(t, rec)
	if created.ID <= 0 {
		t.Fatalf("id = %d, want a positive id", created.ID)
	}
	if created.BookID != 1 || created.MemberID != 7 {
		t.Fatalf("book_id = %d, member_id = %d, want 1 and 7", created.BookID, created.MemberID)
	}
	if !created.BorrowedAt.Equal(borrowedAt) {
		t.Fatalf("borrowed_at = %s, want %s", created.BorrowedAt, borrowedAt)
	}
	if got := created.BorrowedAt.Format(time.RFC3339); got != "2024-05-01T12:30:00Z" {
		t.Fatalf("borrowed_at = %s, want 2024-05-01T12:30:00Z", got)
	}
	if got := created.DueAt.Sub(created.BorrowedAt); got != DefaultLoanPeriod {
		t.Fatalf("due_at - borrowed_at = %v, want %v", got, DefaultLoanPeriod)
	}
	if want := borrowedAt.Add(DefaultLoanPeriod); !created.DueAt.Equal(want) {
		t.Fatalf("due_at = %s, want %s", created.DueAt, want)
	}
	if got := created.DueAt.Format(time.RFC3339); got != "2024-05-15T12:30:00Z" {
		t.Fatalf("due_at = %s, want 2024-05-15T12:30:00Z", got)
	}

	stored := storedLoans(api.loans)
	if len(stored) != 1 {
		t.Fatalf("store holds %d loans, want 1", len(stored))
	}
	saved := stored[0]
	if saved.ID != created.ID || saved.BookID != created.BookID || saved.MemberID != created.MemberID {
		t.Fatalf("stored loan %+v does not match response %+v", saved, created)
	}
	if !saved.BorrowedAt.Equal(created.BorrowedAt) || !saved.DueAt.Equal(created.DueAt) {
		t.Fatalf("stored loan %+v does not match response %+v", saved, created)
	}

	memberRequests := members.served()
	if len(memberRequests) != 1 || memberRequests[0].method != http.MethodGet || memberRequests[0].path != "/members/7" {
		t.Fatalf("members service saw %+v, want one GET /members/7", memberRequests)
	}
	bookRequests := books.served()
	if len(bookRequests) != 1 || bookRequests[0].method != http.MethodPost || bookRequests[0].path != "/books/1/borrow" {
		t.Fatalf("books service saw %+v, want one POST /books/1/borrow", bookRequests)
	}
}

func TestCreateLoanReturns404WhenTheBookDoesNotExist(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusNotFound)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	rec := postLoan(t, router, `{"book_id":999,"member_id":7}`)
	assertErrorResponse(t, rec, http.StatusNotFound, "book not found")

	if got := api.loans.count(); got != 0 {
		t.Fatalf("store holds %d loans, want 0", got)
	}
}

func TestCreateLoanReturns409WhenTheBookIsAlreadyOut(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusConflict)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	rec := postLoan(t, router, `{"book_id":1,"member_id":7}`)
	assertErrorResponse(t, rec, http.StatusConflict, "book already on loan")

	if got := api.loans.count(); got != 0 {
		t.Fatalf("store holds %d loans, want 0", got)
	}
}

func TestCreateLoanReturns404WhenTheMemberDoesNotExist(t *testing.T) {
	members := newFakeUpstream(t, http.StatusNotFound)
	books := newFakeUpstream(t, http.StatusOK)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	rec := postLoan(t, router, `{"book_id":1,"member_id":7}`)
	assertErrorResponse(t, rec, http.StatusNotFound, "member not found")

	if got := api.loans.count(); got != 0 {
		t.Fatalf("store holds %d loans, want 0", got)
	}
	if got := books.served(); len(got) != 0 {
		t.Fatalf("books service saw %+v, want no borrow request", got)
	}
}

func TestCreateLoanReturns502WhenADependencyIsDown(t *testing.T) {
	t.Run("members", func(t *testing.T) {
		members := newFakeUpstream(t, http.StatusOK)
		books := newFakeUpstream(t, http.StatusOK)
		membersBase := baseURL(t, members.server)
		members.server.Close()

		router, api := newLoanTestRouter(membersBase, baseURL(t, books.server))

		rec := postLoan(t, router, `{"book_id":1,"member_id":7}`)
		assertErrorResponse(t, rec, http.StatusBadGateway, "upstream unavailable")

		if got := api.loans.count(); got != 0 {
			t.Fatalf("store holds %d loans, want 0", got)
		}
		if got := books.served(); len(got) != 0 {
			t.Fatalf("books service saw %+v, want no borrow request", got)
		}
	})

	t.Run("books", func(t *testing.T) {
		members := newFakeUpstream(t, http.StatusOK)
		books := newFakeUpstream(t, http.StatusOK)
		booksBase := baseURL(t, books.server)
		books.server.Close()

		router, api := newLoanTestRouter(baseURL(t, members.server), booksBase)

		rec := postLoan(t, router, `{"book_id":1,"member_id":7}`)
		assertErrorResponse(t, rec, http.StatusBadGateway, "upstream unavailable")

		if got := api.loans.count(); got != 0 {
			t.Fatalf("store holds %d loans, want 0", got)
		}
	})
}

func TestCreateLoanRejectsAnInvalidBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"book_id":`},
		{name: "missing book_id", body: `{"member_id":7}`},
		{name: "zero member_id", body: `{"book_id":1,"member_id":0}`},
		{name: "negative book_id", body: `{"book_id":-1,"member_id":7}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			members := newFakeUpstream(t, http.StatusOK)
			books := newFakeUpstream(t, http.StatusOK)
			router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

			rec := postLoan(t, router, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body)
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding error body from %s: %v", rec.Body, err)
			}
			if body.Error == "" {
				t.Fatalf("body %s carries no error message", rec.Body)
			}

			if got := members.served(); len(got) != 0 {
				t.Fatalf("members service saw %+v, want no request", got)
			}
			if got := books.served(); len(got) != 0 {
				t.Fatalf("books service saw %+v, want no request", got)
			}
			if got := api.loans.count(); got != 0 {
				t.Fatalf("store holds %d loans, want 0", got)
			}
		})
	}
}

func TestCreateLoanForwardsTheRequestID(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusOK)
	router, _ := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	req := httptest.NewRequest(http.MethodPost, "/loans", strings.NewReader(`{"book_id":1,"member_id":7}`))
	req.Header.Set(RequestIDHeader, "abc")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body)
	}

	seen := map[string][]upstreamRequest{
		"members": members.served(),
		"books":   books.served(),
	}
	for name, requests := range seen {
		if len(requests) != 1 {
			t.Fatalf("%s service saw %d requests, want 1", name, len(requests))
		}
		if got := requests[0].requestID; got != "abc" {
			t.Fatalf("%s service saw %s = %q, want %q", name, RequestIDHeader, got, "abc")
		}
	}
}
