package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// DefaultLoanPeriod is how long a loan runs: due_at is borrowed_at plus this.
const DefaultLoanPeriod = 14 * 24 * time.Hour

// Loan is one recorded borrow. There is no status field: the due date implies it.
type Loan struct {
	ID         int64     `json:"id"`
	BookID     int64     `json:"book_id"`
	MemberID   int64     `json:"member_id"`
	BorrowedAt time.Time `json:"borrowed_at"`
	DueAt      time.Time `json:"due_at"`
}

// createLoanRequest is the body POST /loans accepts.
type createLoanRequest struct {
	BookID   int64 `json:"book_id"`
	MemberID int64 `json:"member_id"`
}

// errorResponse is the body shape every non-2xx response uses.
type errorResponse struct {
	Error string `json:"error"`
}

// loanStore keeps the recorded loans in memory.
type loanStore struct {
	mu     sync.Mutex
	nextID int64
	loans  []Loan
}

// newLoanStore returns an empty store whose first loan gets id 1.
func newLoanStore() *loanStore {
	return &loanStore{nextID: 1}
}

// record appends a loan for the book and member, giving it the next id and deriving
// the due date from borrowedAt.
func (s *loanStore) record(bookID, memberID int64, borrowedAt time.Time) Loan {
	borrowed := borrowedAt.UTC().Truncate(time.Second)
	loan := Loan{
		BookID:     bookID,
		MemberID:   memberID,
		BorrowedAt: borrowed,
		DueAt:      borrowed.Add(DefaultLoanPeriod),
	}

	s.mu.Lock()
	loan.ID = s.nextID
	s.nextID++
	s.loans = append(s.loans, loan)
	s.mu.Unlock()

	return loan
}

// count reports how many loans have been recorded.
func (s *loanStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.loans)
}

// loanAPI serves the loan endpoints.
type loanAPI struct {
	loans   *loanStore
	members *membersClient
	books   *booksClient
	now     func() time.Time
}

// newLoanAPI wires the handler to its store and the two upstream clients. The clock
// is a field so a test can pin it.
func newLoanAPI(loans *loanStore, members *membersClient, books *booksClient) *loanAPI {
	return &loanAPI{loans: loans, members: members, books: books, now: time.Now}
}

// createLoan serves POST /loans: it validates the body, checks the member, takes the
// book out through the books service and only then records the loan. The member is
// checked first so a book is never marked on loan for a member that does not exist.
func (a *loanAPI) createLoan(w http.ResponseWriter, r *http.Request) {
	var req createLoanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BookID <= 0 || req.MemberID <= 0 {
		writeError(w, http.StatusBadRequest, "book_id and member_id are required")
		return
	}

	ctx := r.Context()
	if err := a.members.memberExists(ctx, req.MemberID); err != nil {
		if errors.Is(err, errMemberNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	if err := a.books.borrow(ctx, req.BookID); err != nil {
		switch {
		case errors.Is(err, errBookNotFound):
			writeError(w, http.StatusNotFound, "book not found")
		case errors.Is(err, errBookOnLoan):
			writeError(w, http.StatusConflict, "book already on loan")
		default:
			writeError(w, http.StatusBadGateway, "upstream unavailable")
		}
		return
	}

	writeJSON(w, http.StatusCreated, a.loans.record(req.BookID, req.MemberID, a.now()))
}

// writeJSON writes body as the JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError writes an errorResponse with the given status.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
