package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

func main() {
	workspaceDir := "."
	newExtractedDir := filepath.Join(workspaceDir, "data", "postgres", "new_extracted")

	// 1. Load industries to map names to IDs
	indRecords, err := readCSVRecords(filepath.Join(newExtractedDir, "industries.csv"))
	if err != nil {
		log.Fatalf("failed to read industries: %v", err)
	}
	indNameToID := make(map[string]string)
	for i := 1; i < len(indRecords); i++ {
		name := strings.TrimSpace(indRecords[i][1])
		id := indRecords[i][0]
		indNameToID[strings.ToLower(name)] = id
	}

	// 2. Define Category to Industry Mapping (based on the user's request to NOT use general)
	categoryToIndustryName := map[string]string{
		"Automotive Franchise":                                                     "Automotive",
		"Consultancy Franchise":                                                    "Business Services",
		"Fitness Franchise":                                                        "Sports & Fitness",
		"Entertainment & Leisure":                                                  "Entertainment",
		"Education Franchise":                                                      "Education",
		"Retail Franchise":                                                         "Retail",
		"Clothing Franchise":                                                       "Fashion",
		"Cleaning Franchise":                                                       "Home-Based Business",
		"Health Care Franchise":                                                    "Health",
		"Icecream Franchise":                                                       "Food & Beverage",
		"Agents, Dealers & Distributors":                                           "Dealers & Distributors",
		"Agents, Dealers &amp; Distributors":                                       "Dealers & Distributors",
		"Jewellery Franchise":                                                      "Retail",
		"Beverage Franchise":                                                       "Food & Beverage",
		"Preschool Franchise":                                                      "Education",
		"Restaurant Franchise":                                                     "Food & Beverage",
		"Financial Service Franchise":                                              "Finance / Banking",
		"Food Franchise":                                                           "Food & Beverage",
		"Real Estate Franchise":                                                    "Real Estate",
		"Pet Franchise":                                                            "Retail",
		"Business Services Franchise":                                              "Business Services",
		"Manufacturing Franchise":                                                  "Logistics / Manufacturing",
		"Construction Franchise":                                                   "Logistics / Manufacturing",
		"Work From Home":                                                           "Home-Based Business",
		"Computer & Internet Franchise":                                            "Technology / IT",
		"Travel Franchise":                                                         "Hotel, Travel & Tourism",
		"Beauty Franchise":                                                         "Beauty",
		"Logistics Franchise":                                                      "Logistics / Manufacturing",
		"Sports Franchise":                                                         "Sports & Fitness",
		"Hotel Franchise":                                                          "Hotel, Travel & Tourism",
		"Consultancy Franchise, Education Franchise, Overseas Education Franchise": "Education",
		"Preschool Franchise, Education Franchise":                                 "Education",
		"Unknown/Not in Excel":                                                     "General",
	}

	categoryToIndustryID := make(map[string]string)
	for cat, indName := range categoryToIndustryName {
		id, ok := indNameToID[strings.ToLower(indName)]
		if !ok {
			log.Fatalf("Industry name not found: %s", indName)
		}
		categoryToIndustryID[strings.ToLower(cat)] = id
	}

	// 3. Load categories.csv
	catRecords, err := readCSVRecords(filepath.Join(newExtractedDir, "categories.csv"))
	if err != nil {
		log.Fatalf("failed to read categories: %v", err)
	}
	catNameToID := make(map[string]string)
	catIDToRecord := make(map[string][]string)
	for i := 1; i < len(catRecords); i++ {
		name := strings.TrimSpace(catRecords[i][2])
		id := catRecords[i][0]
		catNameToID[strings.ToLower(name)] = id
		catIDToRecord[id] = catRecords[i]

		// Update industry_id if it's currently General but we have a better mapping
		currIndustry := catRecords[i][1]
		if currIndustry == "00000000-0000-0000-0000-000000000000" {
			if betterIndustryID, ok := categoryToIndustryID[strings.ToLower(name)]; ok && betterIndustryID != "00000000-0000-0000-0000-000000000000" {
				catRecords[i][1] = betterIndustryID
				fmt.Printf("Updated category %q industry from General to %s\n", name, nameOfIndustry(betterIndustryID, indRecords))
			}
		}
	}

	// 4. Load sub_categories.csv
	subRecords, err := readCSVRecords(filepath.Join(newExtractedDir, "sub_categories.csv"))
	if err != nil {
		log.Fatalf("failed to read sub_categories: %v", err)
	}
	subNameToID := make(map[string]string)
	subIDToRecord := make(map[string][]string)
	for i := 1; i < len(subRecords); i++ {
		name := strings.TrimSpace(subRecords[i][2])
		id := subRecords[i][0]
		subNameToID[strings.ToLower(name)] = id
		subIDToRecord[id] = subRecords[i]
	}

	// 5. Load franchises.csv
	franRecords, err := readCSVRecords(filepath.Join(newExtractedDir, "franchises.csv"))
	if err != nil {
		log.Fatalf("failed to read franchises: %v", err)
	}
	franMap := make(map[string]string) // ID -> Name
	for i := 1; i < len(franRecords); i++ {
		franMap[franRecords[i][0]] = franRecords[i][1]
	}

	// 6. Load franchise_categories.csv
	fcRecords, err := readCSVRecords(filepath.Join(newExtractedDir, "franchise_categories.csv"))
	if err != nil {
		log.Fatalf("failed to read franchise_categories: %v", err)
	}

	// 7. Load Excel for matching
	xlFile, err := excelize.OpenFile(filepath.Join(workspaceDir, "docs", "FranchiseBazar Extracted Dataset.xlsx"))
	if err != nil {
		log.Fatalf("failed to open excel: %v", err)
	}
	defer xlFile.Close()

	rows, err := xlFile.GetRows("Sheet1")
	if err != nil {
		log.Fatalf("failed to get rows: %v", err)
	}

	excelMap := make(map[string][]string) // Cleaned Brand Name -> Row
	for r := 1; r < len(rows); r++ {
		if len(rows[r]) > 0 {
			brandClean := cleanBrandName(rows[r][0])
			excelMap[brandClean] = rows[r]
		}
	}

	// 8. Reconcile
	newCategories := make(map[string][]string)
	newSubCategories := make(map[string][]string)

	categoryUpdates := 0
	subCategoryUpdates := 0

	for i := 1; i < len(fcRecords); i++ {
		fc := fcRecords[i]
		franID := fc[1]
		catID := fc[2]
		subID := fc[3]

		brandName := franMap[franID]
		xlRow, hasExcel := excelMap[cleanBrandName(brandName)]
		var xlCat, xlSubCat string
		if hasExcel {
			if len(xlRow) > 9 {
				xlCat = strings.TrimSpace(xlRow[9])
			}
			if len(xlRow) > 10 {
				xlSubCat = strings.TrimSpace(xlRow[10])
			}
		}

		// Reconcile Category ID
		if _, exists := catIDToRecord[catID]; !exists {
			if xlCat == "" {
				xlCat = "Unknown/Not in Excel"
			}
			// Look up by name
			if correctCatID, foundByName := catNameToID[strings.ToLower(xlCat)]; foundByName {
				fc[2] = correctCatID // Update to existing category's UUID
				catID = correctCatID
				categoryUpdates++
			} else {
				// Add as new category using this ID!
				indID, ok := categoryToIndustryID[strings.ToLower(xlCat)]
				if !ok {
					indID = indNameToID["general"]
				}
				slug := strings.ToLower(strings.ReplaceAll(xlCat, " ", "-"))
				slug = strings.ReplaceAll(slug, "&", "and")
				slug = strings.ReplaceAll(slug, ",", "")

				newCatRec := []string{
					catID,
					indID,
					xlCat,
					slug,
					"", // icon_name
					fmt.Sprintf("/FranchiseHomePage/Category_Icons/%s.png", slug),
					fmt.Sprintf("/FranchiseData/CategoryImage/%s.png", strings.ReplaceAll(xlCat, " ", "%20")),
					"",    // description
					"100", // display_order
					"TRUE",
					"", // meta_title
					"", // meta_description
					"2025-1-1 0:00",
					"2025-1-1 0:00",
				}
				catIDToRecord[catID] = newCatRec
				catNameToID[strings.ToLower(xlCat)] = catID
				newCategories[catID] = newCatRec
				fmt.Printf("Added new category %q (ID: %s, Industry: %s)\n", xlCat, catID, nameOfIndustry(indID, indRecords))
			}
		}

		// Reconcile Sub-Category ID
		if subID != "" {
			if _, exists := subIDToRecord[subID]; !exists {
				if xlSubCat == "" {
					xlSubCat = "Others"
				}
				// Look up by name
				if correctSubID, foundByName := subNameToID[strings.ToLower(xlSubCat)]; foundByName {
					fc[3] = correctSubID // Update to existing subcategory's UUID
					subCategoryUpdates++
				} else {
					// Add as new subcategory using this ID!
					slug := strings.ToLower(strings.ReplaceAll(xlSubCat, " ", "-"))
					slug = strings.ReplaceAll(slug, "&", "and")
					slug = strings.ReplaceAll(slug, ",", "")

					newSubRec := []string{
						subID,
						catID,
						xlSubCat,
						slug,
						"",    // description
						"100", // display_order
						"TRUE",
						"2025-1-1 0:00",
						"2025-1-1 0:00",
					}
					subIDToRecord[subID] = newSubRec
					subNameToID[strings.ToLower(xlSubCat)] = subID
					newSubCategories[subID] = newSubRec
					fmt.Printf("Added new sub-category %q (ID: %s, Parent Category ID: %s)\n", xlSubCat, subID, catID)
				}
			}
		}
	}

	fmt.Printf("\nDone reconciling! Updates made:\n")
	fmt.Printf("- Mapped %d franchise_categories category IDs to correct UUIDs\n", categoryUpdates)
	fmt.Printf("- Mapped %d franchise_categories subcategory IDs to correct UUIDs\n", subCategoryUpdates)
	fmt.Printf("- Created %d new category definitions\n", len(newCategories))
	fmt.Printf("- Created %d new sub-category definitions\n", len(newSubCategories))

	// Write out categories.csv
	fmt.Println("\nWriting updated categories.csv...")
	catOutFile, err := os.Create(filepath.Join(newExtractedDir, "categories.csv"))
	if err != nil {
		log.Fatalf("failed to create categories.csv: %v", err)
	}
	defer catOutFile.Close()

	catWriter := csv.NewWriter(catOutFile)
	if err := catWriter.Write(catRecords[0]); err != nil {
		log.Fatalf("failed to write header: %v", err)
	}

	// First write existing updated ones
	writtenCats := make(map[string]bool)
	for i := 1; i < len(catRecords); i++ {
		id := catRecords[i][0]
		if err := catWriter.Write(catRecords[i]); err != nil {
			log.Fatalf("failed to write category row: %v", err)
		}
		writtenCats[id] = true
	}
	// Write new ones
	for id, rec := range newCategories {
		if !writtenCats[id] {
			if err := catWriter.Write(rec); err != nil {
				log.Fatalf("failed to write new category row: %v", err)
			}
		}
	}
	catWriter.Flush()

	// Write out sub_categories.csv
	fmt.Println("Writing updated sub_categories.csv...")
	subOutFile, err := os.Create(filepath.Join(newExtractedDir, "sub_categories.csv"))
	if err != nil {
		log.Fatalf("failed to create sub_categories.csv: %v", err)
	}
	defer subOutFile.Close()

	subWriter := csv.NewWriter(subOutFile)
	if err := subWriter.Write(subRecords[0]); err != nil {
		log.Fatalf("failed to write sub header: %v", err)
	}

	writtenSubs := make(map[string]bool)
	for i := 1; i < len(subRecords); i++ {
		id := subRecords[i][0]
		if err := subWriter.Write(subRecords[i]); err != nil {
			log.Fatalf("failed to write subcategory row: %v", err)
		}
		writtenSubs[id] = true
	}
	// Write new ones
	for id, rec := range newSubCategories {
		if !writtenSubs[id] {
			if err := subWriter.Write(rec); err != nil {
				log.Fatalf("failed to write new subcategory row: %v", err)
			}
		}
	}
	subWriter.Flush()

	// Write out franchise_categories.csv
	fmt.Println("Writing updated franchise_categories.csv...")
	fcOutFile, err := os.Create(filepath.Join(newExtractedDir, "franchise_categories.csv"))
	if err != nil {
		log.Fatalf("failed to create franchise_categories.csv: %v", err)
	}
	defer fcOutFile.Close()

	fcWriter := csv.NewWriter(fcOutFile)
	for _, rec := range fcRecords {
		if err := fcWriter.Write(rec); err != nil {
			log.Fatalf("failed to write franchise_category row: %v", err)
		}
	}
	fcWriter.Flush()
	fmt.Println("🎉 Reconciled CSV data files successfully updated!")
}

func cleanBrandName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "")
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	return name
}

func nameOfIndustry(id string, indRecords [][]string) string {
	for i := 1; i < len(indRecords); i++ {
		if indRecords[i][0] == id {
			return indRecords[i][1]
		}
	}
	return "Unknown"
}

func readCSVRecords(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	var records [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}
