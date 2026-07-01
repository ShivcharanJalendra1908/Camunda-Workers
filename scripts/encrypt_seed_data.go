//go:build ignore

package main

import (
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"camunda-workers/internal/crypto"
)

// A static dev key for local development (must be 64 bytes total: 32 AES + 32 HMAC)
// 64 bytes of zeros, base64 encoded
var devKey = base64.StdEncoding.EncodeToString(make([]byte, 64))

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: go run encrypt_seed_data.go <path_to_listings.csv>")
	}
	csvPath := os.Args[1]

	encryptor, err := crypto.NewEncryptor(devKey)
	if err != nil {
		log.Fatalf("Failed to create encryptor: %v", err)
	}

	file, err := os.Open(csvPath)
	if err != nil {
		log.Fatalf("Failed to open CSV: %v", err)
	}

	reader := csv.NewReader(file)
	// We might have commas in quotes, encoding/csv handles it correctly
	records, err := reader.ReadAll()
	file.Close()
	if err != nil {
		log.Fatalf("Failed to read CSV: %v", err)
	}

	if len(records) == 0 {
		log.Fatalf("CSV is empty")
	}

	headers := records[0]
	emailIdx := -1
	for i, h := range headers {
		if h == "contact_email" || h == "email" {
			emailIdx = i
			break
		}
	}

	if emailIdx == -1 {
		log.Fatalf("Could not find 'contact_email' or 'email' column")
	}

	encryptedCount := 0
	for i := 1; i < len(records); i++ {
		email := strings.TrimSpace(records[i][emailIdx])
		if email != "" && !strings.HasPrefix(email, "encrypted:") { // avoid double encrypt
			encryptedEmail, err := encryptor.Encrypt([]byte(email))
			if err != nil {
				log.Fatalf("Failed to encrypt email at row %d: %v", i+1, err)
			}
			records[i][emailIdx] = encryptedEmail
			encryptedCount++
		}
	}

	outFile, err := os.Create(csvPath)
	if err != nil {
		log.Fatalf("Failed to create output CSV: %v", err)
	}
	defer outFile.Close()

	writer := csv.NewWriter(outFile)
	err = writer.WriteAll(records)
	if err != nil {
		log.Fatalf("Failed to write updated CSV: %v", err)
	}
	writer.Flush()

	fmt.Printf("Successfully encrypted %d emails in %s\n", encryptedCount, filepath.Base(csvPath))
	fmt.Printf("DEV_ENCRYPTION_KEY used: %s\n", devKey)
}
