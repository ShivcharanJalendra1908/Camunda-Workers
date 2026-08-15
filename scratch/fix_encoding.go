package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"
)

func main() {
	replacements := map[string]string{
		"â€”": "—", // em dash
		"â€“": "–", // en dash
		"â€™": "'", // right single quote / apostrophe
		"â€˜": "'", // left single quote
		"â€œ": "\"", // left double quote
		"â€":  "\"", // right double quote (partial, carefully replace others first)
		"â€¦": "...", // ellipsis
	}

	csvDir := "data/postgres/v2-data"
	files, err := filepath.Glob(filepath.Join(csvDir, "*.csv"))
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		contentBytes, err := ioutil.ReadFile(file)
		if err != nil {
			log.Printf("Error reading %s: %v", file, err)
			continue
		}

		content := string(contentBytes)
		modified := false

		// To avoid â€ replacing part of â€œ, we should process longer strings first, but maps in Go are unordered.
		// Let's do it in a specific order:
		order := []string{"â€”", "â€“", "â€™", "â€˜", "â€œ", "â€¦", "â€"}
		
		for _, bad := range order {
			good := replacements[bad]
			if strings.Contains(content, bad) {
				content = strings.ReplaceAll(content, bad, good)
				modified = true
			}
		}

		if modified {
			err = ioutil.WriteFile(file, []byte(content), 0644)
			if err != nil {
				log.Printf("Error writing %s: %v", file, err)
			} else {
				fmt.Printf("Fixed encoding issues in %s\n", filepath.Base(file))
			}
		}
	}
}
