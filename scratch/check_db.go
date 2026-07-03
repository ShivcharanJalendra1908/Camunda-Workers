package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

func main() {
	connStr := "postgres://lemici:lemici@localhost:5432/lemici_db?sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	var industryID string
	err = db.QueryRow("SELECT industry_id FROM categories WHERE name = 'Trade & Industry'").Scan(&industryID)
	if err != nil {
		fmt.Printf("QueryRow failed: %v\n", err)
		return
	}
	fmt.Printf("Trade & Industry -> industry_id: %s\n", industryID)

	// List all tables
	rows, err := db.Query(`
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = 'public'
	`)
	if err != nil {
		log.Fatalf("failed to list tables: %v", err)
	}
	defer rows.Close()

	fmt.Println("Tables in franchises db:")
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err == nil {
			fmt.Println("-", tableName)
		}
	}
}
