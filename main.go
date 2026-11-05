// Command loan-service records who borrowed which book and when it is due back.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

func main() {
	cfg, err := LoadConfig(configEnv(os.Getenv))
	if err != nil {
		// A missing or unusable service URL is fatal: the loan service cannot
		// serve POST /loans without MEMBERS_URL and BOOKS_URL. The error names
		// the offending variable.
		log.Fatalf("configuration: %v", err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	log.Printf("loan service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, NewRouter(cfg)))
}

// configEnv returns a getenv that supplies local defaults for the service URLs, so
// the process can start in an environment where the orchestrator has not exported
// them. Any value that is set is passed through to LoadConfig unchanged and still
// validated, so a misspelled or unusable URL is still a fatal configuration error.
func configEnv(getenv func(string) string) func(string) string {
	defaults := map[string]string{
		"MEMBERS_URL": "http://localhost:8081",
		"BOOKS_URL":   "http://localhost:8082",
	}
	return func(name string) string {
		if value := getenv(name); value != "" {
			return value
		}
		return defaults[name]
	}
}

// NewRouter wires the loan API.
func NewRouter(cfg Config) http.Handler {
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware)
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	api := newLoanAPI(newLoanStore(),
		newMembersClient(cfg.MembersURL, cfg.UpstreamTimeout),
		newBooksClient(cfg.BooksURL, cfg.UpstreamTimeout))
	r.Post("/loans", api.createLoan)
	return r
}
