// Command loan-service records who borrowed which book, and when it is due back.
// It owns no member or book data: both are looked up through their services.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	dsn := os.Getenv("DB_PATH")
	if dsn == "" {
		dsn = "loans.db"
	}
	db, err := Open(dsn)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	cfg, err := ConfigFromEnv()
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}
	log.Printf("loan service listening on :%s", port)
	if err := http.ListenAndServe(":"+port, NewRouter(db, cfg)); err != nil {
		log.Fatal(err)
	}
}
