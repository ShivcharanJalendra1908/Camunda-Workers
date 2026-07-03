package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

func main() {
	url := "http://localhost:9200/franchise_listings/_search"
	query := `{
		"query": {
			"term": {
				"slug": "aigmf"
			}
		}
	}`

	req, err := http.NewRequestWithContext(context.Background(), "POST", url, strings.NewReader(query))
	if err != nil {
		log.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("failed to read response body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Fatalf("failed to unmarshal: %v", err)
	}

	hits, ok := result["hits"].(map[string]interface{})
	if !ok {
		fmt.Printf("No hits in response: %s\n", string(body))
		return
	}

	hitsList, ok := hits["hits"].([]interface{})
	if !ok || len(hitsList) == 0 {
		fmt.Printf("No matching documents found in Elasticsearch for slug 'aigmf'\n")
		return
	}

	fmt.Println("Elasticsearch Document for AIGMF:")
	doc := hitsList[0].(map[string]interface{})
	source := doc["_source"].(map[string]interface{})
	pretty, _ := json.MarshalIndent(source, "", "  ")
	fmt.Println(string(pretty))
}
