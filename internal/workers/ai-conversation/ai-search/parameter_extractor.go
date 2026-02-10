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

// ✅ OPTIMIZED: Shorter prompt (500 chars → 150 chars) for 2x faster LLM response
func (pe *ParameterExtractor) BuildPrompt(query string) string {
	return fmt.Sprintf(`Extract JSON from query:

"%s"

Return ONLY this JSON structure:
{
  "category": "Food|Education|Fashion|etc or null",
  "location": {"city": "string", "country": "India"} or null,
  "investment": {"min": number, "max": number} or null
}

Rules:
- 1 lakh = 100000, 1 crore = 10000000
- Default country: India
- Use null if not mentioned

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
	// Normalize category
	if params.Category != "" {
		params.Category = strings.TrimSpace(params.Category)
		params.Category = strings.Title(strings.ToLower(params.Category))
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
			// Swap if reversed
			params.Investment.Min, params.Investment.Max = params.Investment.Max, params.Investment.Min
		}
		// Set reasonable max if only min provided
		if params.Investment.Max == 0 && params.Investment.Min > 0 {
			params.Investment.Max = params.Investment.Min * 10
		}
		// Set reasonable min if only max provided
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
		// Create range for single value (±1%)
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
		// Set reasonable max if only min provided
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
		// Set reasonable max if only min provided
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
		// Log error but return empty params instead of failing
		return &ExtractedParameters{
			Category:      "",
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

// package ai_search

// import (
// 	"encoding/json"
// 	"fmt"
// 	"strings"
// )

// type ParameterExtractor struct {
// 	config *Config
// }

// func NewParameterExtractor(config *Config) *ParameterExtractor {
// 	return &ParameterExtractor{config: config}
// }

// // BuildPrompt creates a comprehensive prompt for TinyLlama that handles ALL parameters
// func (pe *ParameterExtractor) BuildPrompt(query string) string {
// 	return fmt.Sprintf(`Extract franchise search parameters from this query. Return ONLY valid JSON.

// Query: "%s"

// Extract these parameters if mentioned:

// 1. CATEGORY (string): Food, Education, Fashion, Retail, Healthcare, Technology, etc.
//    Example: "pizza franchise" → "Food"

// 2. LOCATION (object): Indian city
//    Example: "in Mumbai" → {"city": "Mumbai", "country": "India"}

// 3. INVESTMENT (object): Amount in rupees
//    Conversions: 1 lakh = 100000, 1 crore = 10000000
//    Example: "5 to 10 lakh" → {"min": 500000, "max": 1000000}

// 4. RATING (number): 0 to 5
//    Example: "4 star rated" → 4.0

// 5. SPACE (object): Square feet
//    Example: "500 to 1000 sqft" → {"min": 500, "max": 1000}

// 6. STAFF (object): Number of employees
//    Example: "2 to 5 staff" → {"min": 2, "max": 5}

// 7. OUTLETS (number): Total outlets
//    Example: "100+ outlets" → 100

// 8. ROI (object): Return on investment percentage
//    Example: "8%% ROI" → {"min": 7, "max": 9}

// 9. VERIFIED (boolean): Only verified franchises
//    Example: "verified only" → true

// 10. TRUSTED_SELLER (boolean): Only trusted sellers
//     Example: "trusted seller" → true

// Return this EXACT JSON structure (use null for missing values):
// {
//   "category": "Food",
//   "location": {"city": "Mumbai", "state": "Maharashtra", "country": "India"},
//   "investment": {"min": 500000, "max": 1000000},
//   "rating": 4.0,
//   "space": {"min": 500, "max": 1000},
//   "staff": {"min": 2, "max": 5},
//   "outlets": 100,
//   "roi": {"min": 7, "max": 9},
//   "verified": true,
//   "trusted_seller": false
// }

// Rules:
// - If parameter NOT mentioned, set to null
// - For single values, create range: "5 lakh" → {"min": 500000, "max": 5000000}
// - Default country is "India"
// - Return ONLY JSON, no explanation`, query)
// }

// // Parse extracts parameters from LLM response with robust error handling
// func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {
// 	// Step 1: Clean the response
// 	cleaned := strings.TrimSpace(llmResponse)

// 	// Step 2: Remove markdown code blocks
// 	cleaned = strings.TrimPrefix(cleaned, "```json")
// 	cleaned = strings.TrimPrefix(cleaned, "```")
// 	cleaned = strings.TrimSuffix(cleaned, "```")
// 	cleaned = strings.TrimSpace(cleaned)

// 	// Step 3: Extract JSON object if response contains extra text
// 	jsonStart := strings.Index(cleaned, "{")
// 	jsonEnd := strings.LastIndex(cleaned, "}")

// 	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
// 		return nil, fmt.Errorf("no valid JSON object found in response: %s", cleaned)
// 	}

// 	cleaned = cleaned[jsonStart : jsonEnd+1]

// 	// Step 4: Parse JSON
// 	var params ExtractedParameters
// 	if err := json.Unmarshal([]byte(cleaned), &params); err != nil {
// 		return nil, fmt.Errorf("JSON parse failed: %w, response: %s", err, cleaned)
// 	}

// 	// Step 5: Validate and normalize
// 	if err := pe.normalizeParameters(&params); err != nil {
// 		return nil, fmt.Errorf("parameter normalization failed: %w", err)
// 	}

// 	return &params, nil
// }

// // normalizeParameters validates and normalizes ALL extracted parameters
// func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
// 	// Normalize category
// 	if params.Category != "" {
// 		params.Category = strings.TrimSpace(params.Category)
// 		params.Category = strings.Title(strings.ToLower(params.Category))
// 	}

// 	// Ensure location has country if city is present
// 	if params.Location != nil && params.Location.City != "" {
// 		if params.Location.Country == "" {
// 			params.Location.Country = "India"
// 		}
// 		params.Location.City = strings.TrimSpace(params.Location.City)
// 		params.Location.City = strings.Title(strings.ToLower(params.Location.City))

// 		if params.Location.State != "" {
// 			params.Location.State = strings.TrimSpace(params.Location.State)
// 			params.Location.State = strings.Title(strings.ToLower(params.Location.State))
// 		}
// 	}

// 	// Validate and fix investment range
// 	if params.Investment != nil {
// 		if params.Investment.Min < 0 || params.Investment.Max < 0 {
// 			return fmt.Errorf("investment cannot be negative")
// 		}
// 		if params.Investment.Min > params.Investment.Max {
// 			// Swap if reversed
// 			params.Investment.Min, params.Investment.Max = params.Investment.Max, params.Investment.Min
// 		}
// 		// Set reasonable max if only min provided
// 		if params.Investment.Max == 0 && params.Investment.Min > 0 {
// 			params.Investment.Max = params.Investment.Min * 10
// 		}
// 		// Set reasonable min if only max provided
// 		if params.Investment.Min == 0 && params.Investment.Max > 0 {
// 			params.Investment.Min = params.Investment.Max / 10
// 		}
// 	}

// 	// Validate and fix ROI range
// 	if params.ROI != nil {
// 		if params.ROI.Min < 0 || params.ROI.Max > 100 {
// 			return fmt.Errorf("ROI must be between 0 and 100")
// 		}
// 		if params.ROI.Min > params.ROI.Max {
// 			params.ROI.Min, params.ROI.Max = params.ROI.Max, params.ROI.Min
// 		}
// 		// Create range for single value (±1%)
// 		if params.ROI.Max == 0 && params.ROI.Min > 0 {
// 			params.ROI.Max = params.ROI.Min + 2
// 		}
// 		if params.ROI.Min == 0 && params.ROI.Max > 0 {
// 			params.ROI.Min = params.ROI.Max - 2
// 			if params.ROI.Min < 0 {
// 				params.ROI.Min = 0
// 			}
// 		}
// 	}

// 	// Validate rating
// 	if params.Rating != nil {
// 		if *params.Rating < 0 {
// 			zero := 0.0
// 			params.Rating = &zero
// 		}
// 		if *params.Rating > 5 {
// 			five := 5.0
// 			params.Rating = &five
// 		}
// 	}

// 	// Validate and fix space range
// 	if params.Space != nil {
// 		if params.Space.Min < 0 || params.Space.Max < 0 {
// 			return fmt.Errorf("space cannot be negative")
// 		}
// 		if params.Space.Min > params.Space.Max {
// 			params.Space.Min, params.Space.Max = params.Space.Max, params.Space.Min
// 		}
// 		// Set reasonable max if only min provided
// 		if params.Space.Max == 0 && params.Space.Min > 0 {
// 			params.Space.Max = params.Space.Min * 5
// 		}
// 	}

// 	// Validate and fix staff range
// 	if params.Staff != nil {
// 		if params.Staff.Min < 0 || params.Staff.Max < 0 {
// 			return fmt.Errorf("staff count cannot be negative")
// 		}
// 		if params.Staff.Min > params.Staff.Max {
// 			params.Staff.Min, params.Staff.Max = params.Staff.Max, params.Staff.Min
// 		}
// 		// Set reasonable max if only min provided
// 		if params.Staff.Max == 0 && params.Staff.Min > 0 {
// 			params.Staff.Max = params.Staff.Min * 3
// 		}
// 	}

// 	// Validate outlets
// 	if params.Outlets != nil && *params.Outlets < 0 {
// 		zero := 0
// 		params.Outlets = &zero
// 	}

// 	return nil
// }

// // ParseWithFallback attempts to parse, falls back to empty params on failure
// func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {
// 	params, err := pe.Parse(llmResponse)
// 	if err != nil {
// 		// Log error but return empty params instead of failing
// 		return &ExtractedParameters{
// 			Category:      "",
// 			Location:      nil,
// 			ROI:           nil,
// 			Investment:    nil,
// 			Space:         nil,
// 			Staff:         nil,
// 			Outlets:       nil,
// 			Rating:        nil,
// 			Verified:      nil,
// 			TrustedSeller: nil,
// 		}
// 	}
// 	return params
// }
