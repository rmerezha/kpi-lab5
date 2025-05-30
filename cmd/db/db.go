package main

import (
	"flag"
	"fmt"
	"github.com/roman-mazur/architecture-practice-4-template/datastore"
	"log"
	"net/http"
)

var (
	dbDir  = flag.String("path", "/var/lib/db/data", "Path to database directory")
	dbSize = flag.Int64("size", 10*datastore.Mi, "Size of database to use")
)

func main() {
	flag.Parse()

	db, err := datastore.Open(*dbDir, *dbSize)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	handler := NewHandler(db)

	fmt.Println("Listening on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatal(err)
	}
}
