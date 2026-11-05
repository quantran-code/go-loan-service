package main

import (
	"encoding/json"
	"errors"
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

// newLoanTestRouter serves POST /loans and POST /loans/{id}/return over the two
// dependency bases through the same request-ID middleware the service uses, and hands
// back the API so a test can read the store the loan is recorded in.
func newLoanTestRouter(membersBase, booksBase *url.URL) (http.Handler, *loanAPI) {
	loans := newLoanStore()
	api := newLoanAPI(loans,
		newMembersClient(membersBase, upstreamTimeout),
		newBooksClient(booksBase, upstreamTimeout))
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware)
	r.Post("/loans", api.createLoan)
	r.Post("/loans/{id}/return", api.returnLoan)
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

// postReturn sends POST path with no body and returns the recorded response.
func postReturn(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
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

func TestReturnLoanMarksItReturned(t *testing.T) {
	store := newLoanStore()
	created := store.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))

	if created.ReturnedAt != nil {
		t.Fatalf("record() set ReturnedAt = %s, want nil", created.ReturnedAt)
	}
	open, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshalling loan: %v", err)
	}
	if strings.Contains(string(open), "returned_at") {
		t.Fatalf("json %s carries returned_at on an open loan", open)
	}

	// One hour ahead of UTC, so the mark proves it is normalised.
	returnedAt := time.Date(2024, time.May, 3, 9, 15, 0, 0, time.FixedZone("BST", 3600))
	returned, err := store.returnLoan(created.ID, returnedAt)
	if err != nil {
		t.Fatalf("returnLoan() error: %v", err)
	}
	want := returnedAt.UTC().Truncate(time.Second)
	if returned.ReturnedAt == nil {
		t.Fatalf("ReturnedAt = nil, want %s", want)
	}
	if !returned.ReturnedAt.Equal(want) {
		t.Fatalf("ReturnedAt = %s, want %s", returned.ReturnedAt, want)
	}
	if got := returned.ReturnedAt.Location(); got != time.UTC {
		t.Fatalf("ReturnedAt location = %s, want UTC", got)
	}

	stored := storedLoans(store)
	if len(stored) != 1 || stored[0].ReturnedAt == nil || !stored[0].ReturnedAt.Equal(want) {
		t.Fatalf("stored loans = %+v, want one loan returned at %s", stored, want)
	}

	body, err := json.Marshal(returned)
	if err != nil {
		t.Fatalf("marshalling loan: %v", err)
	}
	if !strings.Contains(string(body), `"returned_at":"2024-05-03T08:15:00Z"`) {
		t.Fatalf("json %s does not carry returned_at in RFC3339", body)
	}
}

func TestReturnLoanRejectsAnAlreadyReturnedLoan(t *testing.T) {
	store := newLoanStore()
	created := store.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))
	firstReturn := time.Date(2024, time.May, 3, 8, 15, 0, 0, time.UTC)

	if _, err := store.returnLoan(created.ID, firstReturn); err != nil {
		t.Fatalf("first returnLoan() error: %v", err)
	}

	if _, err := store.returnLoan(created.ID, firstReturn.Add(48*time.Hour)); !errors.Is(err, errLoanAlreadyReturned) {
		t.Fatalf("second returnLoan() = %v, want %v", err, errLoanAlreadyReturned)
	}

	stored := storedLoans(store)
	if len(stored) != 1 || stored[0].ReturnedAt == nil || !stored[0].ReturnedAt.Equal(firstReturn) {
		t.Fatalf("stored loans = %+v, want one loan returned at %s", stored, firstReturn)
	}
}

func TestReturnLoanRejectsAnUnknownID(t *testing.T) {
	store := newLoanStore()
	created := store.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))

	returned, err := store.returnLoan(created.ID+1, time.Date(2024, time.May, 3, 8, 15, 0, 0, time.UTC))
	if !errors.Is(err, errLoanNotFound) {
		t.Fatalf("returnLoan() = %v, want %v", err, errLoanNotFound)
	}
	if returned != (Loan{}) {
		t.Fatalf("returnLoan() = %+v, want the zero loan", returned)
	}

	stored := storedLoans(store)
	if len(stored) != 1 || stored[0].ReturnedAt != nil {
		t.Fatalf("stored loans = %+v, want one open loan", stored)
	}
}

func TestUndoReturnRestoresTheLoan(t *testing.T) {
	store := newLoanStore()
	created := store.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))
	returnedAt := time.Date(2024, time.May, 3, 8, 15, 0, 0, time.UTC)

	if _, err := store.returnLoan(created.ID, returnedAt); err != nil {
		t.Fatalf("returnLoan() error: %v", err)
	}

	// An unknown id is a no-op, so the marked loan stays as it was.
	store.undoReturn(created.ID + 1)
	if stored := storedLoans(store); len(stored) != 1 || stored[0].ReturnedAt == nil {
		t.Fatalf("stored loans = %+v, want the loan still returned", stored)
	}

	store.undoReturn(created.ID)
	if stored := storedLoans(store); len(stored) != 1 || stored[0].ReturnedAt != nil {
		t.Fatalf("stored loans = %+v, want ReturnedAt cleared", stored)
	}

	returned, err := store.returnLoan(created.ID, returnedAt)
	if err != nil {
		t.Fatalf("returnLoan() after undoReturn error: %v", err)
	}
	if returned.ReturnedAt == nil || !returned.ReturnedAt.Equal(returnedAt) {
		t.Fatalf("ReturnedAt = %v, want %s", returned.ReturnedAt, returnedAt)
	}
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

func TestReturnLoanReleasesTheBook(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusOK)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	borrow := postLoan(t, router, `{"book_id":1,"member_id":7}`)
	if borrow.Code != http.StatusCreated {
		t.Fatalf("borrow status = %d, want %d; body: %s", borrow.Code, http.StatusCreated, borrow.Body)
	}
	created := decodeLoan(t, borrow)

	returnedAt := time.Date(2024, time.May, 3, 8, 15, 0, 0, time.UTC)
	api.now = func() time.Time { return returnedAt }

	rec := postReturn(t, router, "/loans/1/return")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	returned := decodeLoan(t, rec)
	if returned.ID != created.ID {
		t.Fatalf("id = %d, want %d", returned.ID, created.ID)
	}
	if returned.ReturnedAt == nil {
		t.Fatalf("returned_at = nil, want %s", returnedAt)
	}
	if !returned.ReturnedAt.Equal(returnedAt) {
		t.Fatalf("returned_at = %s, want %s", returned.ReturnedAt, returnedAt)
	}
	if got := returned.ReturnedAt.Format(time.RFC3339); got != "2024-05-03T08:15:00Z" {
		t.Fatalf("returned_at = %s, want 2024-05-03T08:15:00Z", got)
	}

	stored := storedLoans(api.loans)
	if len(stored) != 1 || stored[0].ReturnedAt == nil || !stored[0].ReturnedAt.Equal(returnedAt) {
		t.Fatalf("stored loans = %+v, want one loan returned at %s", stored, returnedAt)
	}

	bookRequests := books.served()
	returns := 0
	for _, request := range bookRequests {
		if request.path != "/books/1/return" {
			continue
		}
		if request.method != http.MethodPost {
			t.Fatalf("return request method = %s, want %s", request.method, http.MethodPost)
		}
		returns++
	}
	if returns != 1 {
		t.Fatalf("books service saw %+v, want exactly one POST /books/1/return", bookRequests)
	}
}

func TestReturnLoanTwiceReturns409(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusOK)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))

	if rec := postLoan(t, router, `{"book_id":1,"member_id":7}`); rec.Code != http.StatusCreated {
		t.Fatalf("borrow status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body)
	}
	if rec := postReturn(t, router, "/loans/1/return"); rec.Code != http.StatusOK {
		t.Fatalf("first return status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	rec := postReturn(t, router, "/loans/1/return")
	assertErrorResponse(t, rec, http.StatusConflict, "loan already returned")

	returns := 0
	for _, request := range books.served() {
		if request.path == "/books/1/return" {
			returns++
		}
	}
	if returns != 1 {
		t.Fatalf("books service saw %d return requests, want 1", returns)
	}

	stored := storedLoans(api.loans)
	if len(stored) != 1 || stored[0].ReturnedAt == nil {
		t.Fatalf("stored loans = %+v, want one returned loan", stored)
	}
}

func TestReturnUnknownLoanReturns404(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "unknown id", path: "/loans/999/return"},
		{name: "non-numeric id", path: "/loans/abc/return"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			members := newFakeUpstream(t, http.StatusOK)
			books := newFakeUpstream(t, http.StatusOK)
			router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))
			api.loans.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))

			rec := postReturn(t, router, tc.path)
			assertErrorResponse(t, rec, http.StatusNotFound, "loan not found")

			if got := books.served(); len(got) != 0 {
				t.Fatalf("books service saw %+v, want no request", got)
			}
			stored := storedLoans(api.loans)
			if len(stored) != 1 || stored[0].ReturnedAt != nil {
				t.Fatalf("stored loans = %+v, want one open loan", stored)
			}
		})
	}
}

func TestReturnLoanMapsBooksErrors(t *testing.T) {
	cases := []struct {
		name        string
		booksStatus int
		unreachable bool
		status      int
		want        string
	}{
		{name: "book not found", booksStatus: http.StatusNotFound, status: http.StatusNotFound, want: "book not found"},
		{name: "book already available", booksStatus: http.StatusConflict, status: http.StatusConflict, want: "book not on loan"},
		{name: "books 500", booksStatus: http.StatusInternalServerError, status: http.StatusBadGateway, want: "upstream unavailable"},
		{name: "books unreachable", unreachable: true, status: http.StatusBadGateway, want: "upstream unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			members := newFakeUpstream(t, http.StatusOK)
			books := newFakeUpstream(t, tc.booksStatus)
			booksBase := baseURL(t, books.server)
			if tc.unreachable {
				books.server.Close()
			}
			router, api := newLoanTestRouter(baseURL(t, members.server), booksBase)

			// The loan is seeded directly: the borrow call would answer with the same
			// status the return call has to map.
			api.loans.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))

			rec := postReturn(t, router, "/loans/1/return")
			assertErrorResponse(t, rec, tc.status, tc.want)

			stored := storedLoans(api.loans)
			if len(stored) != 1 || stored[0].ReturnedAt != nil {
				t.Fatalf("stored loans = %+v, want one open loan", stored)
			}
		})
	}
}

func TestReturnLoanForwardsTheRequestID(t *testing.T) {
	members := newFakeUpstream(t, http.StatusOK)
	books := newFakeUpstream(t, http.StatusOK)
	router, api := newLoanTestRouter(baseURL(t, members.server), baseURL(t, books.server))
	api.loans.record(1, 7, time.Date(2024, time.May, 1, 12, 30, 0, 0, time.UTC))

	req := httptest.NewRequest(http.MethodPost, "/loans/1/return", nil)
	req.Header.Set(RequestIDHeader, "abc")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}

	requests := books.served()
	if len(requests) != 1 {
		t.Fatalf("books service saw %d requests, want 1", len(requests))
	}
	if requests[0].method != http.MethodPost || requests[0].path != "/books/1/return" {
		t.Fatalf("books service saw %+v, want POST /books/1/return", requests[0])
	}
	if got := requests[0].requestID; got != "abc" {
		t.Fatalf("books service saw %s = %q, want %q", RequestIDHeader, got, "abc")
	}
}
