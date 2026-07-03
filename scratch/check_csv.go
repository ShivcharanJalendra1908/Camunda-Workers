package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
)

func main() {
	f, err := os.Open("data/postgres/v2-data/listings.csv")
	if err != nil {
		log.Fatalf("failed to open file: %v", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		log.Fatalf("failed to read csv: %v", err)
	}

	header := records[0]
	for _, row := range records[1:] {
		if row[2] == "aigmf" {
			for i, val := range row {
				fmt.Printf("%s: %s\n", header[i], val)
			}
			break
		}
	}
}
