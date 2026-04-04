package ai_search

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type ParameterExtractor struct {
	config *Config
}

func NewParameterExtractor(config *Config) *ParameterExtractor {
	return &ParameterExtractor{config: config}
}

// BuildPrompt - Modelfile mein TEMPLATE + SYSTEM already set hai
// Ollama automatically ChatML wrap karta hai jab /api/generate call hoti hai
// Isliye sirf plain query bhejna hai — server.py bhi yahi karta tha internally
//
//	func (pe *ParameterExtractor) BuildPrompt(query string) string {
//		return query
//	}
func (pe *ParameterExtractor) BuildPrompt(query string) string {
	q := strings.TrimSpace(query)
	// Single word hai toh franchise context add karo
	if len(strings.Fields(q)) == 1 {
		return q + " franchise"
	}
	return q
}

// ftModelOutput - Fine-tuned model ka exact output schema (notebook se)
type ftModelOutput struct {
	Error             interface{} `json:"error"`
	Industry          interface{} `json:"Industry"`
	Category          interface{} `json:"Category"`
	Subcategory       interface{} `json:"Subcategory"`
	Location          interface{} `json:"Location"`
	MinimumInvestment interface{} `json:"Minimum_Investment"`
	MaximumInvestment interface{} `json:"Maximum_Investment"`
	AreaRequirement   interface{} `json:"Area_Requirement"`
	ROI               interface{} `json:"ROI"`
}

// Parse - Fine-tuned model ke flat schema ko Go ke ExtractedParameters mein convert karta hai
func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {

	// ✅ ADD THIS — raw LLM response dekho
	fmt.Printf("🔍 RAW LLM RESPONSE: %s\n", llmResponse)

	// Step 1: Clean markdown artifacts
	cleaned := strings.TrimSpace(llmResponse)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	// Step 2: Extract JSON object
	jsonStart := strings.Index(cleaned, "{")
	jsonEnd := strings.LastIndex(cleaned, "}")
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON found in: %s", cleaned)
	}
	cleaned = cleaned[jsonStart : jsonEnd+1]

	// Step 3: Parse into fine-tuned model's schema
	var ftOut ftModelOutput
	if err := json.Unmarshal([]byte(cleaned), &ftOut); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w, raw: %s", err, cleaned)
	}

	// Step 4: Check for OOD (out of domain)
	if errVal, ok := ftOut.Error.(string); ok && strings.Contains(strings.ToLower(errVal), "out of domain") {
		return &ExtractedParameters{}, nil // Empty params = no search refinement
	}

	// Step 5: Map flat schema → ExtractedParameters
	params := &ExtractedParameters{}

	// Industry
	// if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
	// 	params.Industry = strings.TrimSpace(v)
	// }
	if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
		category := ""
		if c, ok := ftOut.Category.(string); ok {
			category = c
		}
		params.Industry = normalizeIndustry(strings.TrimSpace(v), category)
	}

	// Category
	if v, ok := ftOut.Category.(string); ok && strings.TrimSpace(v) != "" {
		params.Category = strings.TrimSpace(v)
	}

	// Subcategory
	if v, ok := ftOut.Subcategory.(string); ok && strings.TrimSpace(v) != "" {
		params.Subcategory = strings.TrimSpace(v)
	}

	// Location - notebook mein string hai (city name directly), Go mein LocationFilter struct
	if v, ok := ftOut.Location.(string); ok && strings.TrimSpace(v) != "" {
		city := strings.TrimSpace(v)
		// Title case normalize
		city = strings.ToUpper(city[:1]) + strings.ToLower(city[1:])
		params.Location = &LocationFilter{
			City:    city,
			Country: "India",
		}
	}

	// Investment - notebook mein Minimum_Investment aur Maximum_Investment alag fields hain
	minInv := toFloat64(ftOut.MinimumInvestment)
	maxInv := toFloat64(ftOut.MaximumInvestment)
	if minInv > 0 || maxInv > 0 {
		inv := &InvestmentFilter{}
		if minInv > 0 {
			inv.Min = minInv
		}
		if maxInv > 0 {
			inv.Max = maxInv
		}
		// Agar sirf max diya hai toh min = max/10
		if inv.Min == 0 && inv.Max > 0 {
			inv.Min = inv.Max / 10
		}
		// Agar sirf min diya hai toh max = min * 5
		if inv.Max == 0 && inv.Min > 0 {
			inv.Max = inv.Min * 5
		}
		params.Investment = inv
	}

	// Area_Requirement → Space field
	areaVal := toFloat64(ftOut.AreaRequirement)
	if areaVal > 0 {
		params.Space = &RangeFilter{
			Min: areaVal * 0.8, // ±20% range
			Max: areaVal * 1.5,
		}
	}

	// ROI
	roiVal := toFloat64(ftOut.ROI)
	if roiVal > 0 {
		if roiVal > 100 {
			roiVal = 100
		}
		params.ROI = &RangeFilter{
			Min: roiVal,
			Max: roiVal + 10,
		}
	}

	// Normalize
	if err := pe.normalizeParameters(params); err != nil {
		return nil, fmt.Errorf("normalization failed: %w", err)
	}

	return params, nil
}

var industryNormalizationMap = map[string]string{
	"automotive":                       "Automotive",
	"beauty":                           "Beauty",
	"health":                           "Health",
	"food & beverage":                  "Food & Beverage",
	"home-based business":              "Home-Based Business",
	"retail":                           "Retail",
	"education":                        "Education",
	"fashion":                          "Fashion",
	"entertainment":                    "Entertainment",
	"business & professional services": "Business Services",
	"education & edtech":               "Education",
	// "health & fitness":                 "Health",
	"entertainment & leisure":          "Entertainment",
	"real estate & property services":  "Real Estate",
	"financial services":               "Finance / Banking",
	"logistics & delivery services":    "Logistics / Manufacturing",
	"agriculture & sustainability":     "Agriculture",
	"hospitality & lodging":            "Hotel, Travel & Tourism",
	"hospitality":                      "Hotel, Travel & Tourism",
	"automobile services":              "Automotive",
	"beauty, personal care & grooming": "Beauty",
	"home services":                    "Home-Based Business",
}

func normalizeIndustry(industry string, category string) string {
	lower := strings.ToLower(strings.TrimSpace(industry))
	catLower := strings.ToLower(strings.TrimSpace(category))

	// ✅ Travel check PEHLE — map se pehle (government + travel category)
	travelKeywords := []string{"resort", "holiday", "tourism", "travel", "hotel", "lodge", "guesthouse", "destination"}

	if lower == "government" {
		for _, kw := range travelKeywords {
			if strings.Contains(catLower, kw) {
				return "Hotel, Travel & Tourism"
			}
		}
		return "Government"
	}

	if lower == "business services" || lower == "business & professional services" {
		for _, kw := range travelKeywords {
			if strings.Contains(catLower, kw) {
				return "Hotel, Travel & Tourism"
			}
		}
		if strings.Contains(catLower, "dealer") || strings.Contains(catLower, "distributor") {
			return "Dealers & Distributors"
		}
		if strings.Contains(catLower, "software") || strings.Contains(catLower, "it service") || strings.Contains(catLower, "tech") {
			return "Technology / IT"
		}
		if strings.Contains(catLower, "advertis") || strings.Contains(catLower, "media") {
			return "Media / Communication"
		}
		return "Business Services"
	}

	if lower == "retail" {
		if strings.Contains(catLower, "fashion") || strings.Contains(catLower, "apparel") || strings.Contains(catLower, "clothing") {
			return "Fashion"
		}
		return "Retail"
	}

	if lower == "health & fitness" {
		fitnessKeywords := []string{
			"gym", "yoga", "pilates", "fitness centre",
			"crossfit", "zumba", "aerobics", "sports",
			"swimming", "martial arts", "boxing",
		}
		for _, kw := range fitnessKeywords {
			if strings.Contains(catLower, kw) {
				return "Sports & Fitness"
			}
		}
		// category match nahi hua toh Health
		return "Health"
	}

	if normalized, ok := industryNormalizationMap[lower]; ok {
		return normalized
	}
	return industry
}

func toFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		val = strings.TrimSpace(strings.ToUpper(val))
		if val == "" || val == "NULL" {
			return 0
		}
		// "20L" → 2000000, "1.5CR" → 15000000
		multiplier := 1.0
		if strings.HasSuffix(val, "CR") {
			multiplier = 10000000
			val = strings.TrimSuffix(val, "CR")
		} else if strings.HasSuffix(val, "L") {
			multiplier = 100000
			val = strings.TrimSuffix(val, "L")
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f * multiplier
		}
		return 0
	}
	return 0
}

// normalizeParameters - validates and normalizes all parameters
func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
	if params.Industry != "" {
		params.Industry = strings.TrimSpace(params.Industry)
	}
	if params.Category != "" {
		params.Category = strings.TrimSpace(params.Category)
	}
	if params.Subcategory != "" {
		params.Subcategory = strings.TrimSpace(params.Subcategory)
	}

	if params.Location != nil && params.Location.City != "" {
		if params.Location.Country == "" {
			params.Location.Country = "India"
		}
		params.Location.City = strings.TrimSpace(params.Location.City)
		if len(params.Location.City) > 0 {
			params.Location.City = strings.ToUpper(params.Location.City[:1]) + strings.ToLower(params.Location.City[1:])
		}
	}

	if params.Investment != nil {
		if params.Investment.Min < 0 {
			params.Investment.Min = 0
		}
		if params.Investment.Max < 0 {
			params.Investment.Max = 0
		}
		if params.Investment.Min > params.Investment.Max && params.Investment.Max > 0 {
			params.Investment.Min, params.Investment.Max = params.Investment.Max, params.Investment.Min
		}
	}

	if params.ROI != nil {
		if params.ROI.Max > 100 {
			params.ROI.Max = 100
		}
		if params.ROI.Min < 0 {
			params.ROI.Min = 0
		}
		if params.ROI.Min > params.ROI.Max {
			params.ROI.Min, params.ROI.Max = params.ROI.Max, params.ROI.Min
		}
	}

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

	if params.Space != nil {
		if params.Space.Min < 0 {
			params.Space.Min = 0
		}
		if params.Space.Min > params.Space.Max {
			params.Space.Min, params.Space.Max = params.Space.Max, params.Space.Min
		}
	}

	if params.Staff != nil {
		if params.Staff.Min < 0 {
			params.Staff.Min = 0
		}
		if params.Staff.Min > params.Staff.Max {
			params.Staff.Min, params.Staff.Max = params.Staff.Max, params.Staff.Min
		}
	}

	if params.Outlets != nil && *params.Outlets < 0 {
		zero := 0
		params.Outlets = &zero
	}

	return nil
}

// ParseWithFallback - parse karo, failure pe empty params return karo
func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {

	// ✅ ADD THIS
	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)

	params, err := pe.Parse(llmResponse)
	if err != nil {
		return &ExtractedParameters{}
	}
	return params
}
