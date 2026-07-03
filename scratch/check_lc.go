package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"
)

func main() {
	f, err := os.Open("data/postgres/v2-data/listing_categories.csv")
	if err != nil {
		log.Fatalf("failed to open file: %v", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		log.Fatalf("failed to read csv: %v", err)
	}

	fmt.Println("Searching for AIGMF ID 50424df9-899b-4ac9-9a5e-a969f6919b1d in listing_categories.csv...")
	found := false
	for lineNum, row := range records {
		for colNum, val := range row {
			if strings.Contains(val, "50424df9-899b-4ac9-9a5e-a969f6919b1d") {
				fmt.Printf("Line %d, Col %d: %v\n", lineNum+1, colNum+1, row)
				found = true
			}
		}
	}
	if !found {
		fmt.Println("Not found in listing_categories.csv!")
	}
}
