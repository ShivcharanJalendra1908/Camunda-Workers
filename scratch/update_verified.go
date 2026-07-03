package main

import (
	"encoding/csv"
	"fmt"
	"os"
)

func main() {
	filePath := "data/postgres/v2-data/listings.csv"
	file, err := os.Open(filePath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		panic(err)
	}

	if len(records) == 0 {
		fmt.Println("No records found")
		return
	}

	header := records[0]
	verifiedIdx := -1
	for i, h := range header {
		if h == "verified" {
			verifiedIdx = i
			break
		}
	}

	if verifiedIdx == -1 {
		fmt.Println("verified column not found")
		return
	}

	updated := 0
	for i := 1; i < len(records); i++ {
		val := records[i][verifiedIdx]
		if val == "true" || val == "t" || val == "1" || val == "TRUE" || val == "" {
			records[i][verifiedIdx] = "false"
			updated++
		}
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		panic(err)
	}
	defer outFile.Close()

	writer := csv.NewWriter(outFile)
	err = writer.WriteAll(records)
	if err != nil {
		panic(err)
	}
	writer.Flush()

	fmt.Printf("Updated %d records to verified = false in listings.csv\n", updated)
}
