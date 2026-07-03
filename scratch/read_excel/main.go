package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"

	"github.com/xuri/excelize/v2"
)

func main() {
	f, err := excelize.OpenFile("../../docs/Plans/Association/Data/Association_Industry_FINAL.xlsx")
	if err != nil {
		log.Fatalf("failed to open excel file: %v", err)
	}
	defer f.Close()

	// Get all sheet names
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		log.Fatalf("no sheets found in the excel file")
	}

	// Read rows from the first sheet
	sheetName := sheets[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		log.Fatalf("failed to get rows from sheet %s: %v", sheetName, err)
	}

	outFile, err := os.Create("c:/Users/lenovo/Desktop/LeMiCi/Camunda-Workers/scratch/output.csv")
	if err != nil {
		log.Fatalf("failed to create output file: %v", err)
	}
	defer outFile.Close()

	w := csv.NewWriter(outFile)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			log.Fatalf("failed to write row to csv: %v", err)
		}
	}
	w.Flush()
	fmt.Printf("Successfully wrote %d rows to output.csv\n", len(rows))
}
