package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
)

func main() {
	// Group mapping provided by user
	groupMap := map[string][]string{
		"Health & Wellness":                {"Health", "Beauty", "Sports & Fitness"},
		"Food & Hospitality":               {"Food & Beverage", "Hotel, Travel & Tourism", "Home-Based Business"},
		"Lifestyle & Consumer":             {"Fashion", "Entertainment", "Retail"},
		"Business & Professional Services": {"Business Services", "Dealers & Distributors", "Government"},
		"Education & Knowledge":            {"Education"},
		"Technology & Media":               {"Technology / IT", "Media / Communication"},
		"Finance & Real Estate":            {"Finance / Banking", "Real Estate"},
		"Trade & Industry":                 {"Automotive", "Logistics / Manufacturing", "Agriculture"},
		"Other":                            {"General"},
	}

	// 1. Read industries.csv to get Industry Name -> ID mapping
	indFile, err := os.Open("data/postgres/v2-data/industries.csv")
	if err != nil {
		log.Fatalf("Failed to open industries: %v", err)
	}
	defer indFile.Close()

	indReader := csv.NewReader(indFile)
	indRecords, err := indReader.ReadAll()
	if err != nil {
		log.Fatalf("Failed to read industries: %v", err)
	}

	indNameToID := make(map[string]string)
	for i, row := range indRecords {
		if i == 0 {
			continue // skip header
		}
		indNameToID[row[1]] = row[0]
	}

	// 2. Generate new category IDs for each Industry based on its Group
	// industryID -> newCategoryID
	indToNewCat := make(map[string]string)
	// industryID -> Category Name
	indToNewCatName := make(map[string]string)

	for groupName, industries := range groupMap {
		for _, indName := range industries {
			indID, ok := indNameToID[indName]
			if !ok {
				log.Fatalf("Industry not found: %s", indName)
			}
			newCatID := uuid.New().String()
			indToNewCat[indID] = newCatID
			indToNewCatName[indID] = groupName
		}
	}

	// 3. Read categories.csv
	catFile, err := os.Open("data/postgres/v2-data/categories.csv")
	if err != nil {
		log.Fatalf("Failed to open categories: %v", err)
	}
	catReader := csv.NewReader(catFile)
	catRecords, err := catReader.ReadAll()
	if err != nil {
		log.Fatalf("Failed to read categories: %v", err)
	}
	catFile.Close()

	var newCatRecords [][]string
	newCatRecords = append(newCatRecords, catRecords[0]) // Header

	// oldCategoryID -> industryID
	oldCatToInd := make(map[string]string)

	for i, row := range catRecords {
		if i == 0 {
			continue
		}
		catID := row[0]
		indID := row[1]
		createdAt := row[12]

		// "2026-06-30" is the timestamp for old association categories
		if strings.HasPrefix(createdAt, "2026-06-30") {
			oldCatToInd[catID] = indID
		} else {
			// Keep franchise categories
			newCatRecords = append(newCatRecords, row)
		}
	}

	// Add the 21 new association categories
	displayOrder := 1
	for indID, newCatID := range indToNewCat {
		name := indToNewCatName[indID]
		slug := strings.ToLower(strings.ReplaceAll(name, " & ", "-"))
		slug = strings.ReplaceAll(slug, " ", "-")
		
		// Set icon based on name (just using a placeholder or default)
		iconUrl := "/FranchiseHomePage/coming-soon.png"
		imgUrl := "/FranchiseData/CategoryImage/coming-soon.png"

		row := []string{
			newCatID,
			indID,
			name,
			slug,
			"", // icon_name
			iconUrl,
			imgUrl,
			"", // description
			fmt.Sprintf("%d", displayOrder),
			"TRUE", // is_active
			"", // meta_title
			"", // meta_description
			"2026-06-30 00:00:00",
			"2026-06-30 00:00:00",
		}
		newCatRecords = append(newCatRecords, row)
		displayOrder++
	}

	// 4. Update listing_categories.csv
	lcFile, err := os.Open("data/postgres/v2-data/listing_categories.csv")
	if err != nil {
		log.Fatalf("Failed to open listing_categories: %v", err)
	}
	lcReader := csv.NewReader(lcFile)
	lcRecords, err := lcReader.ReadAll()
	if err != nil {
		log.Fatalf("Failed to read listing_categories: %v", err)
	}
	lcFile.Close()

	var newLcRecords [][]string
	newLcRecords = append(newLcRecords, lcRecords[0])

	updatesMade := 0
	for i, row := range lcRecords {
		if i == 0 {
			continue
		}
		catID := row[2]
		if indID, exists := oldCatToInd[catID]; exists {
			// It's an old association category, update it!
			if newCatID, hasNew := indToNewCat[indID]; hasNew {
				row[2] = newCatID
				updatesMade++
			}
		}
		newLcRecords = append(newLcRecords, row)
	}

	// 5. Write back to CSVs
	outCatFile, err := os.Create("data/postgres/v2-data/categories.csv")
	if err != nil {
		log.Fatalf("Failed to create categories.csv: %v", err)
	}
	defer outCatFile.Close()
	catWriter := csv.NewWriter(outCatFile)
	catWriter.WriteAll(newCatRecords)

	outLcFile, err := os.Create("data/postgres/v2-data/listing_categories.csv")
	if err != nil {
		log.Fatalf("Failed to create listing_categories.csv: %v", err)
	}
	defer outLcFile.Close()
	lcWriter := csv.NewWriter(outLcFile)
	lcWriter.WriteAll(newLcRecords)

	fmt.Printf("Successfully generated %d new association categories.\n", len(indToNewCat))
	fmt.Printf("Removed %d old association categories.\n", len(oldCatToInd))
	fmt.Printf("Updated %d listings in listing_categories.csv.\n", updatesMade)
}
