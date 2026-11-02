package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeServices stands in for the two upstream services: a member that exists and
// a book with one copy on the shelf.
type fakeServices struct {
	memberStatus   int
	reserveStatus  int
	reserveCalls   atomic.Int32
	releaseCalls   atomic.Int32
	shelfAvailable atomic.Int32
	memberSrv      *httptest.Server
	catalogSrv     *httptest.Server
}

func newFakeServices(t *testing.T) *fakeServices {
	t.Helper()
	f := &fakeServices{memberStatus: http.StatusOK, reserveStatus: http.StatusOK}
	f.shelfAvailable.Store(1)
	f.memberSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(f.memberStatus)
	}))
	f.catalogSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/reserve"):
			f.reserveCalls.Add(1)
			if f.reserveStatus != http.StatusOK {
				w.WriteHeader(f.reserveStatus)
				return
			}
			f.shelfAvailable.Add(-1)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/release"):
			f.releaseCalls.Add(1)
			f.shelfAvailable.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.memberSrv.Close)
	t.Cleanup(f.catalogSrv.Close)
	return f
}

func newTestRouter(t *testing.T, f *fakeServices) http.Handler {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return NewRouter(db, Config{MemberURL: f.memberSrv.URL, CatalogURL: f.catalogSrv.URL, LoanDays: 14})
}

func do(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestBorrowThenReturn(t *testing.T) {
	f := newFakeServices(t)
	router := newTestRouter(t, f)

	rec := do(t, router, http.MethodPost, "/loans", `{"member_id":1,"book_id":10}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("borrow: %d %s", rec.Code, rec.Body.String())
	}
	var loan struct {
		ID       int64  `json:"id"`
		MemberID int64  `json:"member_id"`
		BookID   int64  `json:"book_id"`
		DueAt    string `json:"due_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loan); err != nil {
		t.Fatal(err)
	}
	if loan.DueAt == "" || f.reserveCalls.Load() != 1 || f.shelfAvailable.Load() != 0 {
		t.Fatalf("unexpected state: loan=%+v reserves=%d shelf=%d", loan, f.reserveCalls.Load(), f.shelfAvailable.Load())
	}

	rec = do(t, router, http.MethodGet, "/loans?member_id=1", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"book_id":10`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, router, http.MethodPost, "/loans/1/return", "")
	if rec.Code != http.StatusOK || f.releaseCalls.Load() != 1 || f.shelfAvailable.Load() != 1 {
		t.Fatalf("return: %d %s release=%d shelf=%d", rec.Code, rec.Body.String(), f.releaseCalls.Load(), f.shelfAvailable.Load())
	}
	rec = do(t, router, http.MethodPost, "/loans/1/return", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second return: %d", rec.Code)
	}
}

func TestUnknownMemberIs404BeforeACopyIsReserved(t *testing.T) {
	f := newFakeServices(t)
	f.memberStatus = http.StatusNotFound
	router := newTestRouter(t, f)

	rec := do(t, router, http.MethodPost, "/loans", `{"member_id":9,"book_id":10}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "member not found") {
		t.Fatalf("unknown member: %d %s", rec.Code, rec.Body.String())
	}
	if f.reserveCalls.Load() != 0 {
		t.Fatalf("a copy was reserved for a missing member")
	}
}

func TestBookWithoutCopiesIs409AndReleasesNothing(t *testing.T) {
	f := newFakeServices(t)
	f.reserveStatus = http.StatusConflict
	router := newTestRouter(t, f)

	rec := do(t, router, http.MethodPost, "/loans", `{"member_id":1,"book_id":10}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no copies available") {
		t.Fatalf("no copies: %d %s", rec.Code, rec.Body.String())
	}
	if f.releaseCalls.Load() != 0 {
		t.Fatalf("a copy was released that was never reserved")
	}
}

func TestASecondActiveLoanIs409AndGivesTheCopyBack(t *testing.T) {
	f := newFakeServices(t)
	router := newTestRouter(t, f)

	if rec := do(t, router, http.MethodPost, "/loans", `{"member_id":1,"book_id":10}`); rec.Code != http.StatusCreated {
		t.Fatalf("first borrow: %d %s", rec.Code, rec.Body.String())
	}
	rec := do(t, router, http.MethodPost, "/loans", `{"member_id":1,"book_id":10}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already has this book") {
		t.Fatalf("second borrow: %d %s", rec.Code, rec.Body.String())
	}
	if f.shelfAvailable.Load() != 0 {
		t.Fatalf("the reserved copy was not returned to the shelf: %d", f.shelfAvailable.Load())
	}
}

func TestValidationAndUnknownLoan(t *testing.T) {
	f := newFakeServices(t)
	router := newTestRouter(t, f)

	for body, want := range map[string]string{
		`{"book_id":10}`:               "member_id is required",
		`{"member_id":1}`:              "book_id is required",
		`{"member_id":1,"book_id":-2}`: "book_id is required",
	} {
		rec := do(t, router, http.MethodPost, "/loans", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, router, http.MethodPost, "/loans/77/return", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown loan: %d", rec.Code)
	}
	if rec := do(t, router, http.MethodGet, "/loans", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing member_id: %d", rec.Code)
	}
}
