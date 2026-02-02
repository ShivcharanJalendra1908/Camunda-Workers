package ai_search

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ParameterExtractor struct {
	config *Config
}

func NewParameterExtractor(config *Config) *ParameterExtractor {
	return &ParameterExtractor{config: config}
}

func (pe *ParameterExtractor) BuildPrompt(query string) string {
	return fmt.Sprintf(`Extract franchise search parameters from query. Return ONLY valid JSON.

Query: "%s"

Parameters:
- category: Food, Education, Fashion, Retail, Healthcare
- location: {city, state, country}
- roi: {min, max} percentage
- investment: {min, max} INR
- space: {min, max} sqft
- staff: {min, max}
- outlets: number
- rating: 0-5
- verified: boolean
- trusted_seller: boolean

Rules:
1. Extract only mentioned parameters
2. For "8%% ROI", use range 7-9 or 8-12
3. Convert: 1 lakh = 100000, 1 crore = 10000000
4. Default country: "India"
5. Return null for missing parameters

JSON format:
{
  "category": "string or null",
  "location": {"city": "string", "state": "string", "country": "India"} or null,
  "roi": {"min": number, "max": number} or null,
  "investment": {"min": number, "max": number} or null,
  "space": {"min": number, "max": number} or null,
  "staff": {"min": number, "max": number} or null,
  "outlets": number or null,
  "rating": number or null,
  "verified": boolean or null,
  "trusted_seller": boolean or null
}`, query)
}

func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {
	cleaned := strings.TrimSpace(llmResponse)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var params ExtractedParameters
	if err := json.Unmarshal([]byte(cleaned), &params); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w", err)
	}

	return &params, nil
}
