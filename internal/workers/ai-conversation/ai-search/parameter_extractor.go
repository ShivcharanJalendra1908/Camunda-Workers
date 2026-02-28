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
	return fmt.Sprintf(`You are a JSON extractor. Extract franchise search parameters from the query below.

Query: "%s"

IMPORTANT RULES:
- industry: MUST be exactly one of these strings:
  "Food & Beverage" → food, cafe, restaurant, chai, coffee, ice cream
  "Sports & Fitness" → gym, fitness, sports, cricket, yoga, workout
  "Beauty" → salon, beauty, spa, hair, makeup
  "Automotive" → auto, car, bike, vehicle, garage, ev
  "Education" → education, school, tutor, coaching
  "Fashion" → fashion, clothes, wear, apparel, saree, kurti
  "Health" → health, clinic, medical, pharmacy, hospital
  "Hotel, Travel & Tourism" → hotel, travel, tourism, holiday, trip, tour
  "Retail" → retail, shop, grocery, kirana, supermarket
  "Technology / IT" → tech, software, it, digital, computer
  "Real Estate" → real estate, property, interior, home, kitchen
  "Finance / Banking" → finance, banking, insurance, loan
  "Entertainment" → entertainment, gaming, kids, toys, pet
  Use null ONLY if no business type mentioned.
- category: specific type like "Gym", "Ice Cream", "Car Wash". null if not specific.
- subcategory: null unless very specific.
- rating: NUMBER only. EXAMPLES: "best gym"→4, "top franchise"→4, "top rated"→4, "highly rated"→4.5. If query has "best" or "top" set rating=4. null if not mentioned.
- investment: "under 10 lakh"={"min":0,"max":1000000}, "under 20 lakh"={"min":0,"max":2000000}, "10-20 lakh"={"min":1000000,"max":2000000}, "1 crore"={"min":5000000,"max":10000000}. null if not mentioned.
- space: {"min": number, "max": number} sqft. null if not mentioned.
- roi: {"min": number, "max": number} percentage. null if not mentioned.
- staff: {"min": number, "max": number}. null if not mentioned.
- verified: true only if explicitly mentioned. null otherwise.
- trusted_seller: true only if explicitly mentioned. null otherwise.
- location: {"city": "city name", "state": "", "country": "India"}

Respond with ONLY this JSON:
{"industry":null,"category":null,"subcategory":null,"location":{"city":"","state":"","country":"India"},"investment":null,"rating":null,"space":null,"staff":null,"outlets":null,"roi":null,"verified":null,"trusted_seller":null}`, query)
}

// func (pe *ParameterExtractor) BuildPrompt(query string) string {
// 	return fmt.Sprintf(`You are a JSON extractor. Extract franchise search parameters from the query below.

// Query: "%s"

// Rules:
// - industry: MUST identify. "food/cafe/restaurant/eat" = "Food & Beverage", "education/school/tutor" = "Education", "fashion/clothes/wear" = "Fashion", "gym/fitness/health/salon/beauty" = "Healthcare & Wellness". Use null ONLY if no business type mentioned.
// - category: Specific sub-type. Use null if not mentioned.
// - location.city: City name if mentioned, else empty string.
// - investment: "under 20 lakh" = {"min":0,"max":2000000}, "under 5 lakh" = {"min":0,"max":500000}, "10 lakh" = {"min":500000,"max":1000000}. null if not mentioned.
// - space: sqft range. "500 sqft" = {"min":500,"max":1000}. null if not mentioned.
// - roi: percentage range. "20 percent roi" = {"min":20,"max":100}. null if not mentioned.
// - rating: minimum rating. "top rated/best" = 4, "highly rated" = 4.5. null if not mentioned.
// - staff: number range. "minimum 5 staff" = {"min":5,"max":50}. null if not mentioned.
// - verified: true only if explicitly asked. null otherwise.
// - Use JSON null (not string "null").

// Respond with ONLY this JSON:
// {"industry":null,"category":null,"subcategory":null,"location":{"city":"","state":"","country":"India"},"investment":null,"rating":null,"space":null,"staff":null,"outlets":null,"roi":null,"verified":null,"trusted_seller":null}`, query)
// }

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
