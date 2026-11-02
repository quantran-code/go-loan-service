package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/quantran-code/go-loan-service/dbgen"
)

// Config is where the other two services live and how long a loan lasts.
type Config struct {
	MemberURL  string
	CatalogURL string
	LoanDays   int
}

// ConfigFromEnv reads the service URLs. LOAN_DAYS defaults to 14.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		MemberURL:  strings.TrimRight(os.Getenv("MEMBER_SERVICE_URL"), "/"),
		CatalogURL: strings.TrimRight(os.Getenv("CATALOG_SERVICE_URL"), "/"),
		LoanDays:   14,
	}
	for _, entry := range []struct{ name, value string }{
		{"MEMBER_SERVICE_URL", cfg.MemberURL},
		{"CATALOG_SERVICE_URL", cfg.CatalogURL},
	} {
		if entry.value == "" {
			return Config{}, errors.New(entry.name + " is required")
		}
	}
	if raw := os.Getenv("LOAN_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 1 {
			return Config{}, errors.New("LOAN_DAYS must be a positive integer")
		}
		cfg.LoanDays = days
	}
	return cfg, nil
}

type loanRequest struct {
	MemberID int64 `json:"member_id"`
	BookID   int64 `json:"book_id"`
}

// NewRouter wires the loan API. The correlation id the gateway set is echoed back
// so a loan can be traced through all three services.
func NewRouter(db *sql.DB, cfg Config) http.Handler {
	queries := dbgen.New(db)
	svc := newServices(cfg.MemberURL, cfg.CatalogURL)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if id := middleware.GetReqID(req.Context()); id != "" {
				w.Header().Set("X-Request-Id", id)
			}
			next.ServeHTTP(w, req)
		})
	})

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Post("/loans", func(w http.ResponseWriter, req *http.Request) {
		var body loanRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.MemberID < 1 {
			writeError(w, http.StatusBadRequest, "member_id is required")
			return
		}
		if body.BookID < 1 {
			writeError(w, http.StatusBadRequest, "book_id is required")
			return
		}

		exists, err := svc.memberExists(req.Context(), body.MemberID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "member service unavailable")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}

		status, err := svc.adjust(req.Context(), body.BookID, -1)
		if err != nil {
			writeError(w, http.StatusBadGateway, "catalog service unavailable")
			return
		}
		switch status {
		case http.StatusOK:
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "book not found")
			return
		case http.StatusConflict:
			writeError(w, http.StatusConflict, "no copies available")
			return
		default:
			writeError(w, http.StatusBadGateway, "catalog service unavailable")
			return
		}

		borrowedAt := time.Now().UTC()
		loan, err := queries.CreateLoan(req.Context(), dbgen.CreateLoanParams{
			MemberID:   body.MemberID,
			BookID:     body.BookID,
			BorrowedAt: borrowedAt,
			DueAt:      borrowedAt.AddDate(0, 0, cfg.LoanDays),
		})
		if err != nil {
			// The unique index rejects a second active loan for the same pair; the
			// copy that was reserved must go back on the shelf.
			_, _ = svc.adjust(context.WithoutCancel(req.Context()), body.BookID, +1)
			writeError(w, http.StatusConflict, "member already has this book")
			return
		}
		writeJSON(w, http.StatusCreated, loan)
	})

	r.Post("/loans/{id}/return", func(w http.ResponseWriter, req *http.Request) {
		id, ok := loanID(w, req)
		if !ok {
			return
		}
		loan, err := queries.GetLoan(req.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "loan not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "loading loan")
			return
		}
		if loan.ReturnedAt.Valid {
			writeError(w, http.StatusConflict, "loan already returned")
			return
		}
		affected, err := queries.MarkReturned(req.Context(), dbgen.MarkReturnedParams{
			ReturnedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
			ID:         id,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "returning loan")
			return
		}
		if affected == 0 {
			writeError(w, http.StatusConflict, "loan already returned")
			return
		}
		if status, err := svc.adjust(req.Context(), loan.BookID, +1); err == nil && status != http.StatusOK {
			writeError(w, http.StatusBadGateway, "catalog service refused the return")
			return
		}
		updated, err := queries.GetLoan(req.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "loading loan")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})

	r.Get("/loans", func(w http.ResponseWriter, req *http.Request) {
		raw := req.URL.Query().Get("member_id")
		if raw == "" {
			writeError(w, http.StatusBadRequest, "member_id is required")
			return
		}
		memberID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || memberID < 1 {
			writeError(w, http.StatusBadRequest, "invalid member id")
			return
		}
		loans, err := queries.ListLoansByMember(req.Context(), memberID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "listing loans")
			return
		}
		if loans == nil {
			loans = []dbgen.Loan{}
		}
		writeJSON(w, http.StatusOK, loans)
	})

	return r
}

func loanID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid loan id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
