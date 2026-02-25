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

// ✅ OPTIMIZED: Full taxonomy-aware prompt with industry/category/subcategory
func (pe *ParameterExtractor) BuildPrompt(query string) string {
	return fmt.Sprintf(`Extract franchise search params from: "%s"

TAXONOMY:
Industry → Category → Subcategory
Examples:
- "Food & Beverage" → "Dessert & Frozen Treats" → "Ice cream parlors"
- "Education" → "Tutoring & Coaching" → "Math learning centers"
- "Fashion" → "Apparel & Clothing Stores" → "Ethnic wear"

Return ONLY valid JSON:
{
  "industry": "Food & Beverage|Education|Fashion|Automotive|etc or null",
  "category": "Category name or null",
  "subcategory": "Subcategory name or null",
  "location": {"city": "string", "state": "string", "country": "India"} or null,
  "investment": {"min": number, "max": number} or null,
  "rating": number (0-5) or null,
  "space": {"min": number, "max": number} sqft or null,
  "staff": {"min": number, "max": number} or null,
  "outlets": number or null,
  "roi": {"min": number, "max": number} percentage or null,
  "verified": boolean or null,
  "trusted_seller": boolean or null
}

RULES:
1. Industry: Main business type (Food & Beverage, Education, Fashion, etc)
2. Category: Sub-industry (Dessert & Frozen Treats, Tutoring & Coaching, etc)
3. Subcategory: Specific type (Ice cream parlors, Math learning centers, etc)
4. Location: Extract city/state if mentioned. Default country: India
5. Investment: Convert lakhs/crores (1L=100000, 1Cr=10000000)
6. Use null if not mentioned

EXAMPLES:
Query: "ice cream franchise in kolkata"
{
  "industry": "Food & Beverage",
  "category": "Dessert & Frozen Treats",
  "subcategory": "Ice cream parlors",
  "location": {"city": "Kolkata", "state": "West Bengal", "country": "India"},
  "investment": null,
  "rating": null,
  "space": null,
  "staff": null,
  "outlets": null,
  "roi": null,
  "verified": null,
  "trusted_seller": null
}

Query: "education franchise under 10 lakh"
{
  "industry": "Education",
  "category": null,
  "subcategory": null,
  "location": null,
  "investment": {"min": 100000, "max": 1000000},
  "rating": null,
  "space": null,
  "staff": null,
  "outlets": null,
  "roi": null,
  "verified": null,
  "trusted_seller": null
}

Query: "fashion boutique in delhi with 500 sqft space"
{
  "industry": "Fashion",
  "category": "Apparel & Clothing Stores",
  "subcategory": null,
  "location": {"city": "Delhi", "state": "Delhi", "country": "India"},
  "investment": null,
  "rating": null,
  "space": {"min": 500, "max": 1000},
  "staff": null,
  "outlets": null,
  "roi": null,
  "verified": null,
  "trusted_seller": null
}

JSON:`, query)
}

// Parse extracts parameters from LLM response with robust error handling
func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {
	// Step 1: Clean the response
	cleaned := strings.TrimSpace(llmResponse)

	// Step 2: Remove markdown code blocks
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	// Step 3: Extract JSON object if response contains extra text
	jsonStart := strings.Index(cleaned, "{")
	jsonEnd := strings.LastIndex(cleaned, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON object found in response: %s", cleaned)
	}

	cleaned = cleaned[jsonStart : jsonEnd+1]

	// Step 4: Parse JSON
	var params ExtractedParameters
	if err := json.Unmarshal([]byte(cleaned), &params); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w, response: %s", err, cleaned)
	}

	// Step 5: Validate and normalize
	if err := pe.normalizeParameters(&params); err != nil {
		return nil, fmt.Errorf("parameter normalization failed: %w", err)
	}

	return &params, nil
}

// normalizeParameters validates and normalizes ALL extracted parameters
func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
	// Normalize industry (capitalize properly)
	if params.Industry != "" {
		params.Industry = strings.TrimSpace(params.Industry)
	}

	// Normalize category
	if params.Category != "" {
		params.Category = strings.TrimSpace(params.Category)
	}

	// Normalize subcategory
	if params.Subcategory != "" {
		params.Subcategory = strings.TrimSpace(params.Subcategory)
	}

	// Ensure location has country if city is present
	if params.Location != nil && params.Location.City != "" {
		if params.Location.Country == "" {
			params.Location.Country = "India"
		}
		params.Location.City = strings.TrimSpace(params.Location.City)
		params.Location.City = strings.Title(strings.ToLower(params.Location.City))

		if params.Location.State != "" {
			params.Location.State = strings.TrimSpace(params.Location.State)
			params.Location.State = strings.Title(strings.ToLower(params.Location.State))
		}
	}

	// Validate and fix investment range
	if params.Investment != nil {
		if params.Investment.Min < 0 || params.Investment.Max < 0 {
			return fmt.Errorf("investment cannot be negative")
		}
		if params.Investment.Min > params.Investment.Max {
			params.Investment.Min, params.Investment.Max = params.Investment.Max, params.Investment.Min
		}
		if params.Investment.Max == 0 && params.Investment.Min > 0 {
			params.Investment.Max = params.Investment.Min * 10
		}
		if params.Investment.Min == 0 && params.Investment.Max > 0 {
			params.Investment.Min = params.Investment.Max / 10
		}
	}

	// Validate and fix ROI range
	if params.ROI != nil {
		if params.ROI.Min < 0 || params.ROI.Max > 100 {
			return fmt.Errorf("ROI must be between 0 and 100")
		}
		if params.ROI.Min > params.ROI.Max {
			params.ROI.Min, params.ROI.Max = params.ROI.Max, params.ROI.Min
		}
		if params.ROI.Max == 0 && params.ROI.Min > 0 {
			params.ROI.Max = params.ROI.Min + 2
		}
		if params.ROI.Min == 0 && params.ROI.Max > 0 {
			params.ROI.Min = params.ROI.Max - 2
			if params.ROI.Min < 0 {
				params.ROI.Min = 0
			}
		}
	}

	// Validate rating
	if params.Rating != nil {
		if *params.Rating < 0 {
			zero := 0.0
			params.Rating = &zero
		}
		if *params.Rating > 5 {
			five := 5.0
			params.Rating = &five
		}
	}

	// Validate and fix space range
	if params.Space != nil {
		if params.Space.Min < 0 || params.Space.Max < 0 {
			return fmt.Errorf("space cannot be negative")
		}
		if params.Space.Min > params.Space.Max {
			params.Space.Min, params.Space.Max = params.Space.Max, params.Space.Min
		}
		if params.Space.Max == 0 && params.Space.Min > 0 {
			params.Space.Max = params.Space.Min * 5
		}
	}

	// Validate and fix staff range
	if params.Staff != nil {
		if params.Staff.Min < 0 || params.Staff.Max < 0 {
			return fmt.Errorf("staff count cannot be negative")
		}
		if params.Staff.Min > params.Staff.Max {
			params.Staff.Min, params.Staff.Max = params.Staff.Max, params.Staff.Min
		}
		if params.Staff.Max == 0 && params.Staff.Min > 0 {
			params.Staff.Max = params.Staff.Min * 3
		}
	}

	// Validate outlets
	if params.Outlets != nil && *params.Outlets < 0 {
		zero := 0
		params.Outlets = &zero
	}

	return nil
}

// ParseWithFallback attempts to parse, falls back to empty params on failure
func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {
	params, err := pe.Parse(llmResponse)
	if err != nil {
		return &ExtractedParameters{
			Industry:      "",
			Category:      "",
			Subcategory:   "",
			Location:      nil,
			ROI:           nil,
			Investment:    nil,
			Space:         nil,
			Staff:         nil,
			Outlets:       nil,
			Rating:        nil,
			Verified:      nil,
			TrustedSeller: nil,
		}
	}
	return params
}
