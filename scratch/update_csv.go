package main

import (
	"encoding/csv"
	"log"
	"os"
)

func main() {
	csvFile := "data/postgres/v2-data/listings.csv"
	tempFile := "data/postgres/v2-data/listings_updated.csv"

	inFile, err := os.Open(csvFile)
	if err != nil {
		log.Fatal(err)
	}
	defer inFile.Close()

	reader := csv.NewReader(inFile)
	records, err := reader.ReadAll()
	if err != nil {
		log.Fatal(err)
	}

	if len(records) > 0 {
		records[0] = append(records[0], "is_featured")
		for i := 1; i < len(records); i++ {
			// entity_type is index 14, status is 15
			if len(records[i]) > 15 && records[i][14] == "blog" && records[i][15] == "live" {
				records[i] = append(records[i], "TRUE")
			} else {
				records[i] = append(records[i], "FALSE")
			}
		}
	}

	outFile, err := os.Create(tempFile)
	if err != nil {
		log.Fatal(err)
	}
	defer outFile.Close()

	writer := csv.NewWriter(outFile)
	err = writer.WriteAll(records)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Successfully wrote to listings_updated.csv. Please manually replace the original file with this one if the original is locked.")
}
