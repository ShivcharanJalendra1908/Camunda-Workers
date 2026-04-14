package ai_search

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"camunda-workers/internal/common/location"
)

type ParameterExtractor struct {
	config *Config
}

func NewParameterExtractor(config *Config) *ParameterExtractor {
	return &ParameterExtractor{config: config}
}

func (pe *ParameterExtractor) BuildPrompt(query string) string {
	q := strings.TrimSpace(query)
	if len(strings.Fields(q)) == 1 {
		return q + " franchise"
	}
	return q
}

// ftModelOutput - Fine-tuned model ka exact output schema
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

// ============================================================
// INDUSTRY NORMALIZATION
// Source: industries.csv (20 industries total)
// Model training data: 11 industries
// ============================================================

var industryNormalizationMap = map[string]string{
	// 11 training data industries
	"automobile services":              "Automotive",
	"beauty, personal care & grooming": "Beauty",
	"education & edtech":               "Education",
	"entertainment & leisure":          "Entertainment",
	"financial services":               "Finance / Banking",
	"food & beverage":                  "Food & Beverage",
	"home services":                    "Home-Based Business",
	"hospitality & lodging":            "Hotel, Travel & Tourism",
	"logistics & delivery services":    "Logistics / Manufacturing",
	"retail":                           "Retail",
	// health & fitness is category-dependent — see normalizeIndustry()

	// Extra aliases
	"automotive":                       "Automotive",
	"beauty":                           "Beauty",
	"education":                        "Education",
	"entertainment":                    "Entertainment",
	"fashion":                          "Fashion",
	"health":                           "Health",
	"sports & fitness":                 "Sports & Fitness",
	"sports":                           "Sports & Fitness",
	"fitness":                          "Sports & Fitness",
	"hospitality":                      "Hotel, Travel & Tourism",
	"hotel, travel & tourism":          "Hotel, Travel & Tourism",
	"travel":                           "Hotel, Travel & Tourism",
	"tourism":                          "Hotel, Travel & Tourism",
	"real estate":                      "Real Estate",
	"real estate & property services":  "Real Estate",
	"business services":                "Business Services",
	"business & professional services": "Business Services",
	"logistics":                        "Logistics / Manufacturing",
	"logistics / manufacturing":        "Logistics / Manufacturing",
	"finance / banking":                "Finance / Banking",
	"finance":                          "Finance / Banking",
	"banking":                          "Finance / Banking",
	"insurance":                        "Finance / Banking",
	"home-based business":              "Home-Based Business",
	"media / communication":            "Media / Communication",
	"media":                            "Media / Communication",
	"government":                       "Government",
	"technology / it":                  "Technology / IT",
	"technology":                       "Technology / IT",
	"it services":                      "Technology / IT",
	"dealers & distributors":           "Dealers & Distributors",
	"dealers":                          "Dealers & Distributors",
	"distributors":                     "Dealers & Distributors",
	"agriculture":                      "Agriculture",
	"agriculture & sustainability":     "Agriculture",
}

// industrySlugMap - ES exact slugs from industries.csv
var industrySlugMap = map[string]string{
	"Automotive":                "automotive",
	"Beauty":                    "beauty",
	"Health":                    "health",
	"Business Services":         "business-services",
	"Dealers & Distributors":    "dealers-distributors",
	"Education":                 "education",
	"Fashion":                   "fashion",
	"Food & Beverage":           "food-beverage",
	"Home-Based Business":       "home-based-business",
	"Hotel, Travel & Tourism":   "hotel-travel-tourism",
	"Retail":                    "retail",
	"Sports & Fitness":          "sports-fitness",
	"Entertainment":             "entertainment",
	"Government":                "government",
	"Real Estate":               "real-estate",
	"Technology / IT":           "technology-it",
	"Finance / Banking":         "finance-banking",
	"Logistics / Manufacturing": "logistics-manufacturing",
	"Agriculture":               "agriculture",
	"Media / Communication":     "media-communication",
}

// GetIndustrySlug - direct slug lookup, no string manipulation
func GetIndustrySlug(industryName string) string {
	if slug, ok := industrySlugMap[industryName]; ok {
		return slug
	}
	slug := strings.ToLower(industryName)
	slug = strings.ReplaceAll(slug, " & ", "-")
	slug = strings.ReplaceAll(slug, " / ", "-")
	slug = strings.ReplaceAll(slug, "&", "")
	slug = strings.ReplaceAll(slug, "/", "")
	slug = strings.ReplaceAll(slug, ",", "")
	slug = strings.ReplaceAll(slug, " ", "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
}

func normalizeIndustry(industry string, category string) string {
	lower := strings.ToLower(strings.TrimSpace(industry))
	catLower := strings.ToLower(strings.TrimSpace(category))

	// Health & Fitness — category se decide karo
	// Fitness Centres & Gyms → Sports & Fitness
	// Medical & Healthcare / Wellness & Nutrition → Health
	if lower == "health & fitness" {
		fitnessKws := []string{
			"fitness centre", "fitness center", "gym", "yoga",
			"pilates", "crossfit", "zumba", "aerobics", "sports",
			"swimming", "martial arts", "boxing", "trampoline",
		}
		for _, kw := range fitnessKws {
			if strings.Contains(catLower, kw) {
				return "Sports & Fitness"
			}
		}
		return "Health"
	}

	// Retail → Apparel/Fashion Retail → Fashion industry
	if lower == "retail" {
		if strings.Contains(catLower, "apparel") ||
			strings.Contains(catLower, "fashion") ||
			strings.Contains(catLower, "clothing") {
			return "Fashion"
		}
		return "Retail"
	}

	// Business Services — subcategory check
	if lower == "business services" || lower == "business & professional services" {
		travelKws := []string{
			"resort", "holiday", "tourism", "travel", "hotel",
			"lodge", "guesthouse", "vacation", "boutique hotel", "homestay",
		}
		for _, kw := range travelKws {
			if strings.Contains(catLower, kw) {
				return "Hotel, Travel & Tourism"
			}
		}
		techKws := []string{"software", "it service", "tech", "technology"}
		for _, kw := range techKws {
			if strings.Contains(catLower, kw) {
				return "Technology / IT"
			}
		}
		if strings.Contains(catLower, "media") || strings.Contains(catLower, "advertis") {
			return "Media / Communication"
		}
		if strings.Contains(catLower, "dealer") || strings.Contains(catLower, "distributor") {
			return "Dealers & Distributors"
		}
		return "Business Services"
	}

	if normalized, ok := industryNormalizationMap[lower]; ok {
		return normalized
	}
	return industry
}

// ============================================================
// HQ DETECTION
// ============================================================

var hqKeywords = []string{
	"headquartered in", "headquarters in", "head office",
	"hq in", "hq hai", "corporate hq", "parent brand",
	"parent company", "brand ka hq", "unka head office",
	"unka hq", "brand office", "main office", "ka corporate",
	"brand is headquartered", "brand ka office",
}

func isHQOnlyLocation(query, locationStr string) bool {
	if locationStr == "" {
		return false
	}
	queryLower := strings.ToLower(query)
	locLower := strings.ToLower(locationStr)
	for _, kw := range hqKeywords {
		idx := strings.Index(queryLower, kw)
		if idx == -1 {
			continue
		}
		after := queryLower[idx:]
		if strings.Contains(after, locLower) {
			return true
		}
	}
	return false
}

// ============================================================
// DUAL LOCATION DETECTION
// ============================================================

var userLocationPatterns = []string{
	"main abhi ", "mera base ", "meri location ",
	"currently based in ", "currently in ",
	"i am based in ", "i am from ", "i'm from ",
	"i'm based in ", "i'm located in ",
	"i live in ", "my current location is ",
	"based in ", "i am currently based in ",
	"hailing from ", "i stay in ", "i reside in ",
	"main ", "mujhe ", "mera ",
}

var targetLocationPatterns = []string{
	"mein franchise lena", "mein franchise chahiye",
	"mein open karna", "mein start karna",
	"mein invest", "mein launch", "mein lena chahta",
	"mein lena chahti", "franchise in ", "franchise at ",
	"want to open", "want to start", "looking to open",
	"planning to open", "planning to launch",
	"planning to start", "want to invest in",
	"open a ", "start a ", "anywhere in ",
	"franchise across ", "i want to open", "i want to start",
}

func extractTargetCity(query string) string {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	allCities := getAllKnownCities()

	for _, city := range allCities {
		cityLower := strings.ToLower(city)
		if !strings.Contains(queryLower, cityLower) {
			continue
		}
		for _, tp := range targetLocationPatterns {
			idx := strings.Index(queryLower, tp)
			if idx == -1 {
				continue
			}
			afterPattern := queryLower[idx:]
			if strings.Contains(afterPattern, cityLower) {
				if !isCityUserLocation(queryLower, cityLower) {
					return city
				}
			}
		}
		for _, prep := range []string{" in ", " at ", " for "} {
			if strings.Contains(queryLower, prep+cityLower) {
				if !isCityUserLocation(queryLower, cityLower) {
					return city
				}
			}
		}
	}
	return ""
}

func isCityUserLocation(queryLower, cityLower string) bool {
	for _, up := range userLocationPatterns {
		idx := strings.Index(queryLower, up)
		if idx == -1 {
			continue
		}
		after := queryLower[idx:]
		cityIdx := strings.Index(after, cityLower)
		if cityIdx == -1 {
			continue
		}
		hasTargetBefore := false
		for _, tp := range targetLocationPatterns {
			tpIdx := strings.Index(after, tp)
			if tpIdx != -1 && tpIdx < cityIdx {
				hasTargetBefore = true
				break
			}
		}
		if !hasTargetBefore {
			return true
		}
	}
	return false
}

func getAllKnownCities() []string {
	cities := []string{}
	seen := map[string]bool{}
	for city := range location.CityAliases {
		title := strings.ToUpper(city[:1]) + city[1:]
		if !seen[strings.ToLower(city)] {
			cities = append(cities, title)
			seen[strings.ToLower(city)] = true
		}
	}
	extra := []string{
		"Noida", "Meerut", "Agra", "Ajmer", "Amritsar", "Aurangabad",
		"Bhopal", "Bhubaneswar", "Chandigarh", "Coimbatore", "Cuttack",
		"Dehradun", "Delhi", "Faridabad", "Guwahati", "Hubli",
		"Indore", "Jabalpur", "Jaipur", "Jodhpur", "Kanpur",
		"Lucknow", "Ludhiana", "Mangalore", "Nagpur", "Nashik",
		"Patna", "Rajkot", "Ranchi", "Surat", "Vadodara",
		"Visakhapatnam", "Goa", "Shimla", "Kochi", "Vijayawada",
		"Tirupati", "Raipur", "Varanasi", "Prayagraj", "Bareilly",
		"Ghaziabad", "Gorakhpur", "Haridwar", "Panipat", "Karnal",
		"Rohtak", "Hisar", "Patiala", "Bathinda", "Mohali",
		"Udaipur", "Kota", "Bikaner", "Bhilai", "Dhanbad",
		"Jamshedpur", "Bokaro", "Rourkela", "Berhampur", "Sambalpur",
		"Madurai", "Tiruchirappalli", "Salem", "Warangal",
		"Kozhikode", "Thrissur", "Kollam", "Hubballi", "Belagavi",
		"Mysuru", "Mangaluru", "Shivamogga",
	}
	for _, c := range extra {
		if !seen[strings.ToLower(c)] {
			cities = append(cities, c)
			seen[strings.ToLower(c)] = true
		}
	}
	return cities
}

// ============================================================
// STATE & ZONE
// ============================================================

var stateNames = map[string]string{
	"andhra pradesh": "Andhra Pradesh", "assam": "Assam",
	"bihar": "Bihar", "chhattisgarh": "Chhattisgarh",
	"delhi ncr": "Delhi NCR", "delhi": "Delhi NCR",
	"goa": "Goa", "gujarat": "Gujarat", "haryana": "Haryana",
	"himachal pradesh": "Himachal Pradesh", "jharkhand": "Jharkhand",
	"karnataka": "Karnataka", "kerala": "Kerala",
	"madhya pradesh": "Madhya Pradesh", "maharashtra": "Maharashtra",
	"odisha": "Odisha", "orissa": "Odisha", "punjab": "Punjab",
	"rajasthan": "Rajasthan", "tamil nadu": "Tamil Nadu",
	"telangana": "Telangana", "uttar pradesh": "Uttar Pradesh",
	"up": "Uttar Pradesh", "uttarakhand": "Uttarakhand",
	"west bengal": "West Bengal",
}

var zoneNames = map[string]bool{
	"north india": true, "south india": true, "east india": true,
	"west india": true, "central india": true,
	"northeast india": true, "pan india": true, "pan-india": true,
}

func (pe *ParameterExtractor) parseLocationString(locStr string) *LocationFilter {
	locLower := strings.ToLower(strings.TrimSpace(locStr))

	if _, ok := zoneNames[locLower]; ok {
		if esZone, ok := location.ZoneKeywords[locLower]; ok {
			return &LocationFilter{City: esZone, Country: "India"}
		}
		return &LocationFilter{City: locStr, Country: "India"}
	}

	if proper, ok := stateNames[locLower]; ok {
		return &LocationFilter{City: proper, State: proper, Country: "India"}
	}

	city := strings.ToUpper(locStr[:1]) + strings.ToLower(locStr[1:])
	return &LocationFilter{City: city, Country: "India"}
}

// ============================================================
// PARSE
// ============================================================

func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {
	fmt.Printf("🔍 RAW LLM RESPONSE: %s\n", llmResponse)

	cleaned := strings.TrimSpace(llmResponse)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	jsonStart := strings.Index(cleaned, "{")
	jsonEnd := strings.LastIndex(cleaned, "}")
	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON found in: %s", cleaned)
	}
	cleaned = cleaned[jsonStart : jsonEnd+1]

	var ftOut ftModelOutput
	if err := json.Unmarshal([]byte(cleaned), &ftOut); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w, raw: %s", err, cleaned)
	}

	if errVal, ok := ftOut.Error.(string); ok && strings.Contains(strings.ToLower(errVal), "out of domain") {
		return &ExtractedParameters{}, nil
	}

	params := &ExtractedParameters{}

	// Category pehle (industry normalization ke liye chahiye)
	category := ""
	if c, ok := ftOut.Category.(string); ok && strings.TrimSpace(c) != "" {
		category = strings.TrimSpace(c)
		params.Category = category
	}

	if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
		params.Industry = normalizeIndustry(strings.TrimSpace(v), category)
	}

	if v, ok := ftOut.Subcategory.(string); ok && strings.TrimSpace(v) != "" {
		params.Subcategory = strings.TrimSpace(v)
	}

	if v, ok := ftOut.Location.(string); ok && strings.TrimSpace(v) != "" {
		params.Location = pe.parseLocationString(strings.TrimSpace(v))
	}

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
		if inv.Min == 0 && inv.Max > 0 {
			inv.Min = inv.Max / 10
		}
		if inv.Max == 0 && inv.Min > 0 {
			inv.Max = inv.Min * 5
		}
		params.Investment = inv
	}

	areaVal := toFloat64(ftOut.AreaRequirement)
	if areaVal > 0 {
		params.Space = &RangeFilter{Min: areaVal * 0.8, Max: areaVal * 1.5}
	}

	roiVal := toFloat64(ftOut.ROI)
	if roiVal > 0 {
		if roiVal > 100 {
			roiVal = 100
		}
		params.ROI = &RangeFilter{Min: roiVal, Max: roiVal + 10}
	}

	if err := pe.normalizeParameters(params); err != nil {
		return nil, fmt.Errorf("normalization failed: %w", err)
	}

	return params, nil
}

// ============================================================
// ParseWithContext — location fix with original query
// CALL THIS from handler.go instead of ParseWithFallback
// ============================================================

func (pe *ParameterExtractor) ParseWithContext(llmResponse string, originalQuery string) *ExtractedParameters {
	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)

	params, err := pe.Parse(llmResponse)
	if err != nil {
		fmt.Printf("⚠️  Parse error: %v\n", err)
		return &ExtractedParameters{}
	}

	if originalQuery == "" {
		return params
	}

	queryLower := strings.ToLower(originalQuery)

	// FIX 1: HQ Location
	// "Brand headquartered in Mumbai, Pune mein lena chahta hun"
	// Model: "Mumbai" → Fix: "Pune"
	if params.Location != nil && params.Location.City != "" {
		if isHQOnlyLocation(originalQuery, params.Location.City) {
			targetCity := extractTargetCity(originalQuery)
			if targetCity != "" {
				fmt.Printf("🏢 HQ fix: '%s' → '%s'\n", params.Location.City, targetCity)
				params.Location = pe.parseLocationString(targetCity)
			} else {
				fmt.Printf("🏢 HQ location cleared\n")
				params.Location = nil
			}
		}
	}

	// FIX 2: Dual Location
	// "Main Noida mein hun, Chennai mein franchise lena chahta hun"
	// Model: "Noida" → Fix: "Chennai"
	if params.Location != nil && params.Location.City != "" {
		cityLower := strings.ToLower(params.Location.City)
		if isCityUserLocation(queryLower, cityLower) {
			targetCity := extractTargetCity(originalQuery)
			if targetCity != "" && strings.ToLower(targetCity) != cityLower {
				fmt.Printf("📍 Dual location fix: '%s' → '%s'\n", params.Location.City, targetCity)
				params.Location = pe.parseLocationString(targetCity)
			}
		}
	}

	// FIX 3: Zone/State — Model null deta hai
	// "East India mein food franchise" → "East India"
	// "West Bengal mein exam prep" → "West Bengal"
	if params.Location == nil || params.Location.City == "" {
		for zone := range zoneNames {
			if strings.Contains(queryLower, zone) {
				if !isCityUserLocation(queryLower, zone) {
					zoneTitled := strings.ToUpper(zone[:1]) + zone[1:]
					params.Location = pe.parseLocationString(zoneTitled)
					fmt.Printf("🗺️  Zone from query: %s\n", zone)
					break
				}
			}
		}
		if params.Location == nil || params.Location.City == "" {
			for stateLower, stateProper := range stateNames {
				if strings.Contains(queryLower, stateLower) {
					if !isCityUserLocation(queryLower, stateLower) {
						params.Location = pe.parseLocationString(stateProper)
						fmt.Printf("🗺️  State from query: %s\n", stateProper)
						break
					}
				}
			}
		}
	}

	return params
}

// ParseWithFallback — backward compatible
func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {
	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)
	params, err := pe.Parse(llmResponse)
	if err != nil {
		return &ExtractedParameters{}
	}
	return params
}

// ============================================================
// toFloat64 — "15L"→1500000, "2Cr"→20000000
// ES investment Lakhs mein stored hai, handler /100000 karta hai
// ============================================================

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

// ============================================================
// normalizeParameters
// ============================================================

func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
	params.Industry = strings.TrimSpace(params.Industry)
	params.Category = strings.TrimSpace(params.Category)
	params.Subcategory = strings.TrimSpace(params.Subcategory)

	if params.Location != nil {
		if params.Location.Country == "" {
			params.Location.Country = "India"
		}
		params.Location.City = strings.TrimSpace(params.Location.City)
		if len(params.Location.City) > 0 {
			params.Location.City = strings.ToUpper(params.Location.City[:1]) +
				strings.ToLower(params.Location.City[1:])
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
			params.Investment.Min, params.Investment.Max =
				params.Investment.Max, params.Investment.Min
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
			z := 0.0
			params.Rating = &z
		}
		if *params.Rating > 5 {
			f := 5.0
			params.Rating = &f
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
		z := 0
		params.Outlets = &z
	}

	return nil
}

// package ai_search

// import (
// 	"encoding/json"
// 	"fmt"
// 	"strconv"
// 	"strings"

// 	"camunda-workers/internal/common/location"
// )

// type ParameterExtractor struct {
// 	config *Config
// }

// func NewParameterExtractor(config *Config) *ParameterExtractor {
// 	return &ParameterExtractor{config: config}
// }

// func (pe *ParameterExtractor) BuildPrompt(query string) string {
// 	q := strings.TrimSpace(query)
// 	if len(strings.Fields(q)) == 1 {
// 		return q + " franchise"
// 	}
// 	return q
// }

// // ftModelOutput - Fine-tuned model ka exact output schema
// type ftModelOutput struct {
// 	Error             interface{} `json:"error"`
// 	Industry          interface{} `json:"Industry"`
// 	Category          interface{} `json:"Category"`
// 	Subcategory       interface{} `json:"Subcategory"`
// 	Location          interface{} `json:"Location"`
// 	MinimumInvestment interface{} `json:"Minimum_Investment"`
// 	MaximumInvestment interface{} `json:"Maximum_Investment"`
// 	AreaRequirement   interface{} `json:"Area_Requirement"`
// 	ROI               interface{} `json:"ROI"`
// }

// // ============================================================
// // INDUSTRY NORMALIZATION
// // Source: industries.csv (20 industries total)
// // Model training data: 11 industries
// // ============================================================

// var industryNormalizationMap = map[string]string{
// 	// 11 training data industries
// 	"automobile services":              "Automotive",
// 	"beauty, personal care & grooming": "Beauty",
// 	"education & edtech":               "Education",
// 	"entertainment & leisure":          "Entertainment",
// 	"financial services":               "Finance / Banking",
// 	"food & beverage":                  "Food & Beverage",
// 	"home services":                    "Home-Based Business",
// 	"hospitality & lodging":            "Hotel, Travel & Tourism",
// 	"logistics & delivery services":    "Logistics / Manufacturing",
// 	"retail":                           "Retail",
// 	// health & fitness is category-dependent — see normalizeIndustry()

// 	// Extra aliases
// 	"automotive":                       "Automotive",
// 	"beauty":                           "Beauty",
// 	"education":                        "Education",
// 	"entertainment":                    "Entertainment",
// 	"fashion":                          "Fashion",
// 	"health":                           "Health",
// 	"sports & fitness":                 "Sports & Fitness",
// 	"sports":                           "Sports & Fitness",
// 	"fitness":                          "Sports & Fitness",
// 	"hospitality":                      "Hotel, Travel & Tourism",
// 	"hotel, travel & tourism":          "Hotel, Travel & Tourism",
// 	"travel":                           "Hotel, Travel & Tourism",
// 	"tourism":                          "Hotel, Travel & Tourism",
// 	"real estate":                      "Real Estate",
// 	"real estate & property services":  "Real Estate",
// 	"business services":                "Business Services",
// 	"business & professional services": "Business Services",
// 	"logistics":                        "Logistics / Manufacturing",
// 	"logistics / manufacturing":        "Logistics / Manufacturing",
// 	"finance / banking":                "Finance / Banking",
// 	"finance":                          "Finance / Banking",
// 	"banking":                          "Finance / Banking",
// 	"insurance":                        "Finance / Banking",
// 	"home-based business":              "Home-Based Business",
// 	"media / communication":            "Media / Communication",
// 	"media":                            "Media / Communication",
// 	"government":                       "Government",
// 	"technology / it":                  "Technology / IT",
// 	"technology":                       "Technology / IT",
// 	"it services":                      "Technology / IT",
// 	"dealers & distributors":           "Dealers & Distributors",
// 	"dealers":                          "Dealers & Distributors",
// 	"distributors":                     "Dealers & Distributors",
// 	"agriculture":                      "Agriculture",
// 	"agriculture & sustainability":     "Agriculture",
// }

// // industrySlugMap - ES exact slugs from industries.csv
// var industrySlugMap = map[string]string{
// 	"Automotive":                "automotive",
// 	"Beauty":                    "beauty",
// 	"Health":                    "health",
// 	"Business Services":         "business-services",
// 	"Dealers & Distributors":    "dealers-distributors",
// 	"Education":                 "education",
// 	"Fashion":                   "fashion",
// 	"Food & Beverage":           "food-beverage",
// 	"Home-Based Business":       "home-based-business",
// 	"Hotel, Travel & Tourism":   "hotel-travel-tourism",
// 	"Retail":                    "retail",
// 	"Sports & Fitness":          "sports-fitness",
// 	"Entertainment":             "entertainment",
// 	"Government":                "government",
// 	"Real Estate":               "real-estate",
// 	"Technology / IT":           "technology-it",
// 	"Finance / Banking":         "finance-banking",
// 	"Logistics / Manufacturing": "logistics-manufacturing",
// 	"Agriculture":               "agriculture",
// 	"Media / Communication":     "media-communication",
// }

// // GetIndustrySlug - direct slug lookup, no string manipulation
// func GetIndustrySlug(industryName string) string {
// 	if slug, ok := industrySlugMap[industryName]; ok {
// 		return slug
// 	}
// 	slug := strings.ToLower(industryName)
// 	slug = strings.ReplaceAll(slug, " & ", "-")
// 	slug = strings.ReplaceAll(slug, " / ", "-")
// 	slug = strings.ReplaceAll(slug, "&", "")
// 	slug = strings.ReplaceAll(slug, "/", "")
// 	slug = strings.ReplaceAll(slug, ",", "")
// 	slug = strings.ReplaceAll(slug, " ", "-")
// 	for strings.Contains(slug, "--") {
// 		slug = strings.ReplaceAll(slug, "--", "-")
// 	}
// 	return slug
// }

// func normalizeIndustry(industry string, category string) string {
// 	lower := strings.ToLower(strings.TrimSpace(industry))
// 	catLower := strings.ToLower(strings.TrimSpace(category))

// 	// Health & Fitness — category se decide karo
// 	// Fitness Centres & Gyms → Sports & Fitness
// 	// Medical & Healthcare / Wellness & Nutrition → Health
// 	if lower == "health & fitness" {
// 		fitnessKws := []string{
// 			"fitness centre", "fitness center", "gym", "yoga",
// 			"pilates", "crossfit", "zumba", "aerobics", "sports",
// 			"swimming", "martial arts", "boxing", "trampoline",
// 		}
// 		for _, kw := range fitnessKws {
// 			if strings.Contains(catLower, kw) {
// 				return "Sports & Fitness"
// 			}
// 		}
// 		return "Health"
// 	}

// 	// Retail → Apparel/Fashion Retail → Fashion industry
// 	if lower == "retail" {
// 		if strings.Contains(catLower, "apparel") ||
// 			strings.Contains(catLower, "fashion") ||
// 			strings.Contains(catLower, "clothing") {
// 			return "Fashion"
// 		}
// 		return "Retail"
// 	}

// 	// Business Services — subcategory check
// 	if lower == "business services" || lower == "business & professional services" {
// 		travelKws := []string{
// 			"resort", "holiday", "tourism", "travel", "hotel",
// 			"lodge", "guesthouse", "vacation", "boutique hotel", "homestay",
// 		}
// 		for _, kw := range travelKws {
// 			if strings.Contains(catLower, kw) {
// 				return "Hotel, Travel & Tourism"
// 			}
// 		}
// 		techKws := []string{"software", "it service", "tech", "technology"}
// 		for _, kw := range techKws {
// 			if strings.Contains(catLower, kw) {
// 				return "Technology / IT"
// 			}
// 		}
// 		if strings.Contains(catLower, "media") || strings.Contains(catLower, "advertis") {
// 			return "Media / Communication"
// 		}
// 		if strings.Contains(catLower, "dealer") || strings.Contains(catLower, "distributor") {
// 			return "Dealers & Distributors"
// 		}
// 		return "Business Services"
// 	}

// 	if normalized, ok := industryNormalizationMap[lower]; ok {
// 		return normalized
// 	}
// 	return industry
// }

// // ============================================================
// // HQ DETECTION
// // ============================================================

// var hqKeywords = []string{
// 	"headquartered in", "headquarters in", "head office",
// 	"hq in", "hq hai", "corporate hq", "parent brand",
// 	"parent company", "brand ka hq", "unka head office",
// 	"unka hq", "brand office", "main office", "ka corporate",
// 	"brand is headquartered", "brand ka office",
// }

// func isHQOnlyLocation(query, locationStr string) bool {
// 	if locationStr == "" {
// 		return false
// 	}
// 	queryLower := strings.ToLower(query)
// 	locLower := strings.ToLower(locationStr)
// 	for _, kw := range hqKeywords {
// 		idx := strings.Index(queryLower, kw)
// 		if idx == -1 {
// 			continue
// 		}
// 		after := queryLower[idx:]
// 		if strings.Contains(after, locLower) {
// 			return true
// 		}
// 	}
// 	return false
// }

// // ============================================================
// // DUAL LOCATION DETECTION
// // ============================================================

// var userLocationPatterns = []string{
// 	"main abhi ", "mera base ", "meri location ",
// 	"currently based in ", "currently in ",
// 	"i am based in ", "i am from ", "i'm from ",
// 	"i'm based in ", "i'm located in ",
// 	"i live in ", "my current location is ",
// 	"based in ", "i am currently based in ",
// 	"hailing from ", "i stay in ", "i reside in ",
// 	"main ", "mujhe ", "mera ",
// }

// var targetLocationPatterns = []string{
// 	"mein franchise lena", "mein franchise chahiye",
// 	"mein open karna", "mein start karna",
// 	"mein invest", "mein launch", "mein lena chahta",
// 	"mein lena chahti", "franchise in ", "franchise at ",
// 	"want to open", "want to start", "looking to open",
// 	"planning to open", "planning to launch",
// 	"planning to start", "want to invest in",
// 	"open a ", "start a ", "anywhere in ",
// 	"franchise across ", "i want to open", "i want to start",
// }

// func extractTargetCity(query string) string {
// 	queryLower := strings.ToLower(strings.TrimSpace(query))
// 	allCities := getAllKnownCities()

// 	for _, city := range allCities {
// 		cityLower := strings.ToLower(city)
// 		if !strings.Contains(queryLower, cityLower) {
// 			continue
// 		}
// 		for _, tp := range targetLocationPatterns {
// 			idx := strings.Index(queryLower, tp)
// 			if idx == -1 {
// 				continue
// 			}
// 			afterPattern := queryLower[idx:]
// 			if strings.Contains(afterPattern, cityLower) {
// 				if !isCityUserLocation(queryLower, cityLower) {
// 					return city
// 				}
// 			}
// 		}
// 		for _, prep := range []string{" in ", " at ", " for "} {
// 			if strings.Contains(queryLower, prep+cityLower) {
// 				if !isCityUserLocation(queryLower, cityLower) {
// 					return city
// 				}
// 			}
// 		}
// 	}
// 	return ""
// }

// func isCityUserLocation(queryLower, cityLower string) bool {
// 	for _, up := range userLocationPatterns {
// 		idx := strings.Index(queryLower, up)
// 		if idx == -1 {
// 			continue
// 		}
// 		after := queryLower[idx:]
// 		cityIdx := strings.Index(after, cityLower)
// 		if cityIdx == -1 {
// 			continue
// 		}
// 		hasTargetBefore := false
// 		for _, tp := range targetLocationPatterns {
// 			tpIdx := strings.Index(after, tp)
// 			if tpIdx != -1 && tpIdx < cityIdx {
// 				hasTargetBefore = true
// 				break
// 			}
// 		}
// 		if !hasTargetBefore {
// 			return true
// 		}
// 	}
// 	return false
// }

// func getAllKnownCities() []string {
// 	cities := []string{}
// 	seen := map[string]bool{}
// 	for city := range location.CityAliases {
// 		title := strings.ToUpper(city[:1]) + city[1:]
// 		if !seen[strings.ToLower(city)] {
// 			cities = append(cities, title)
// 			seen[strings.ToLower(city)] = true
// 		}
// 	}
// 	extra := []string{
// 		"Noida", "Meerut", "Agra", "Ajmer", "Amritsar", "Aurangabad",
// 		"Bhopal", "Bhubaneswar", "Chandigarh", "Coimbatore", "Cuttack",
// 		"Dehradun", "Delhi", "Faridabad", "Guwahati", "Hubli",
// 		"Indore", "Jabalpur", "Jaipur", "Jodhpur", "Kanpur",
// 		"Lucknow", "Ludhiana", "Mangalore", "Nagpur", "Nashik",
// 		"Patna", "Rajkot", "Ranchi", "Surat", "Vadodara",
// 		"Visakhapatnam", "Goa", "Shimla", "Kochi", "Vijayawada",
// 		"Tirupati", "Raipur", "Varanasi", "Prayagraj", "Bareilly",
// 		"Ghaziabad", "Gorakhpur", "Haridwar", "Panipat", "Karnal",
// 		"Rohtak", "Hisar", "Patiala", "Bathinda", "Mohali",
// 		"Udaipur", "Kota", "Bikaner", "Bhilai", "Dhanbad",
// 		"Jamshedpur", "Bokaro", "Rourkela", "Berhampur", "Sambalpur",
// 		"Madurai", "Tiruchirappalli", "Salem", "Warangal",
// 		"Kozhikode", "Thrissur", "Kollam", "Hubballi", "Belagavi",
// 		"Mysuru", "Mangaluru", "Shivamogga",
// 	}
// 	for _, c := range extra {
// 		if !seen[strings.ToLower(c)] {
// 			cities = append(cities, c)
// 			seen[strings.ToLower(c)] = true
// 		}
// 	}
// 	return cities
// }

// // ============================================================
// // STATE & ZONE
// // ============================================================

// var stateNames = map[string]string{
// 	"andhra pradesh": "Andhra Pradesh", "assam": "Assam",
// 	"bihar": "Bihar", "chhattisgarh": "Chhattisgarh",
// 	"delhi ncr": "Delhi NCR", "delhi": "Delhi NCR",
// 	"goa": "Goa", "gujarat": "Gujarat", "haryana": "Haryana",
// 	"himachal pradesh": "Himachal Pradesh", "jharkhand": "Jharkhand",
// 	"karnataka": "Karnataka", "kerala": "Kerala",
// 	"madhya pradesh": "Madhya Pradesh", "maharashtra": "Maharashtra",
// 	"odisha": "Odisha", "orissa": "Odisha", "punjab": "Punjab",
// 	"rajasthan": "Rajasthan", "tamil nadu": "Tamil Nadu",
// 	"telangana": "Telangana", "uttar pradesh": "Uttar Pradesh",
// 	"up": "Uttar Pradesh", "uttarakhand": "Uttarakhand",
// 	"west bengal": "West Bengal",
// }

// var zoneNames = map[string]bool{
// 	"north india": true, "south india": true, "east india": true,
// 	"west india": true, "central india": true,
// 	"northeast india": true, "pan india": true, "pan-india": true,
// }

// func (pe *ParameterExtractor) parseLocationString(locStr string) *LocationFilter {
// 	locLower := strings.ToLower(strings.TrimSpace(locStr))

// 	if _, ok := zoneNames[locLower]; ok {
// 		if esZone, ok := location.ZoneKeywords[locLower]; ok {
// 			return &LocationFilter{City: esZone, Country: "India"}
// 		}
// 		return &LocationFilter{City: locStr, Country: "India"}
// 	}

// 	if proper, ok := stateNames[locLower]; ok {
// 		return &LocationFilter{City: proper, State: proper, Country: "India"}
// 	}

// 	city := strings.ToUpper(locStr[:1]) + strings.ToLower(locStr[1:])
// 	return &LocationFilter{City: city, Country: "India"}
// }

// // ============================================================
// // PARSE
// // ============================================================

// func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {
// 	fmt.Printf("🔍 RAW LLM RESPONSE: %s\n", llmResponse)

// 	cleaned := strings.TrimSpace(llmResponse)
// 	cleaned = strings.TrimPrefix(cleaned, "```json")
// 	cleaned = strings.TrimPrefix(cleaned, "```")
// 	cleaned = strings.TrimSuffix(cleaned, "```")
// 	cleaned = strings.TrimSpace(cleaned)

// 	jsonStart := strings.Index(cleaned, "{")
// 	jsonEnd := strings.LastIndex(cleaned, "}")
// 	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
// 		return nil, fmt.Errorf("no valid JSON found in: %s", cleaned)
// 	}
// 	cleaned = cleaned[jsonStart : jsonEnd+1]

// 	var ftOut ftModelOutput
// 	if err := json.Unmarshal([]byte(cleaned), &ftOut); err != nil {
// 		return nil, fmt.Errorf("JSON parse failed: %w, raw: %s", err, cleaned)
// 	}

// 	if errVal, ok := ftOut.Error.(string); ok && strings.Contains(strings.ToLower(errVal), "out of domain") {
// 		return &ExtractedParameters{}, nil
// 	}

// 	params := &ExtractedParameters{}

// 	// Category pehle (industry normalization ke liye chahiye)
// 	category := ""
// 	if c, ok := ftOut.Category.(string); ok && strings.TrimSpace(c) != "" {
// 		category = strings.TrimSpace(c)
// 		params.Category = category
// 	}

// 	if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
// 		params.Industry = normalizeIndustry(strings.TrimSpace(v), category)
// 	}

// 	if v, ok := ftOut.Subcategory.(string); ok && strings.TrimSpace(v) != "" {
// 		params.Subcategory = strings.TrimSpace(v)
// 	}

// 	if v, ok := ftOut.Location.(string); ok && strings.TrimSpace(v) != "" {
// 		params.Location = pe.parseLocationString(strings.TrimSpace(v))
// 	}

// 	minInv := toFloat64(ftOut.MinimumInvestment)
// 	maxInv := toFloat64(ftOut.MaximumInvestment)
// 	if minInv > 0 || maxInv > 0 {
// 		inv := &InvestmentFilter{}
// 		if minInv > 0 {
// 			inv.Min = minInv
// 		}
// 		if maxInv > 0 {
// 			inv.Max = maxInv
// 		}
// 		if inv.Min == 0 && inv.Max > 0 {
// 			inv.Min = inv.Max / 10
// 		}
// 		if inv.Max == 0 && inv.Min > 0 {
// 			inv.Max = inv.Min * 5
// 		}
// 		params.Investment = inv
// 	}

// 	areaVal := toFloat64(ftOut.AreaRequirement)
// 	if areaVal > 0 {
// 		params.Space = &RangeFilter{Min: areaVal * 0.8, Max: areaVal * 1.5}
// 	}

// 	roiVal := toFloat64(ftOut.ROI)
// 	if roiVal > 0 {
// 		if roiVal > 100 {
// 			roiVal = 100
// 		}
// 		params.ROI = &RangeFilter{Min: roiVal, Max: roiVal + 10}
// 	}

// 	if err := pe.normalizeParameters(params); err != nil {
// 		return nil, fmt.Errorf("normalization failed: %w", err)
// 	}

// 	return params, nil
// }

// // ============================================================
// // ParseWithContext — location fix with original query
// // CALL THIS from handler.go instead of ParseWithFallback
// // ============================================================

// func (pe *ParameterExtractor) ParseWithContext(llmResponse string, originalQuery string) *ExtractedParameters {
// 	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)

// 	params, err := pe.Parse(llmResponse)
// 	if err != nil {
// 		fmt.Printf("⚠️  Parse error: %v\n", err)
// 		return &ExtractedParameters{}
// 	}

// 	if originalQuery == "" {
// 		return params
// 	}

// 	queryLower := strings.ToLower(originalQuery)

// 	// FIX 1: HQ Location
// 	// "Brand headquartered in Mumbai, Pune mein lena chahta hun"
// 	// Model: "Mumbai" → Fix: "Pune"
// 	if params.Location != nil && params.Location.City != "" {
// 		if isHQOnlyLocation(originalQuery, params.Location.City) {
// 			targetCity := extractTargetCity(originalQuery)
// 			if targetCity != "" {
// 				fmt.Printf("🏢 HQ fix: '%s' → '%s'\n", params.Location.City, targetCity)
// 				params.Location = pe.parseLocationString(targetCity)
// 			} else {
// 				fmt.Printf("🏢 HQ location cleared\n")
// 				params.Location = nil
// 			}
// 		}
// 	}

// 	// FIX 2: Dual Location
// 	// "Main Noida mein hun, Chennai mein franchise lena chahta hun"
// 	// Model: "Noida" → Fix: "Chennai"
// 	if params.Location != nil && params.Location.City != "" {
// 		cityLower := strings.ToLower(params.Location.City)
// 		if isCityUserLocation(queryLower, cityLower) {
// 			targetCity := extractTargetCity(originalQuery)
// 			if targetCity != "" && strings.ToLower(targetCity) != cityLower {
// 				fmt.Printf("📍 Dual location fix: '%s' → '%s'\n", params.Location.City, targetCity)
// 				params.Location = pe.parseLocationString(targetCity)
// 			}
// 		}
// 	}

// 	// FIX 3: Zone/State — Model null deta hai
// 	// "East India mein food franchise" → "East India"
// 	// "West Bengal mein exam prep" → "West Bengal"
// 	if params.Location == nil || params.Location.City == "" {
// 		for zone := range zoneNames {
// 			if strings.Contains(queryLower, zone) {
// 				if !isCityUserLocation(queryLower, zone) {
// 					zoneTitled := strings.ToUpper(zone[:1]) + zone[1:]
// 					params.Location = pe.parseLocationString(zoneTitled)
// 					fmt.Printf("🗺️  Zone from query: %s\n", zone)
// 					break
// 				}
// 			}
// 		}
// 		if params.Location == nil || params.Location.City == "" {
// 			for stateLower, stateProper := range stateNames {
// 				if strings.Contains(queryLower, stateLower) {
// 					if !isCityUserLocation(queryLower, stateLower) {
// 						params.Location = pe.parseLocationString(stateProper)
// 						fmt.Printf("🗺️  State from query: %s\n", stateProper)
// 						break
// 					}
// 				}
// 			}
// 		}
// 	}

// 	return params
// }

// // ParseWithFallback — backward compatible
// func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {
// 	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)
// 	params, err := pe.Parse(llmResponse)
// 	if err != nil {
// 		return &ExtractedParameters{}
// 	}
// 	return params
// }

// // ============================================================
// // toFloat64 — "15L"→1500000, "2Cr"→20000000
// // ES investment Lakhs mein stored hai, handler /100000 karta hai
// // ============================================================

// func toFloat64(v interface{}) float64 {
// 	if v == nil {
// 		return 0
// 	}
// 	switch val := v.(type) {
// 	case float64:
// 		return val
// 	case int:
// 		return float64(val)
// 	case int64:
// 		return float64(val)
// 	case string:
// 		val = strings.TrimSpace(strings.ToUpper(val))
// 		if val == "" || val == "NULL" {
// 			return 0
// 		}
// 		multiplier := 1.0
// 		if strings.HasSuffix(val, "CR") {
// 			multiplier = 10000000
// 			val = strings.TrimSuffix(val, "CR")
// 		} else if strings.HasSuffix(val, "L") {
// 			multiplier = 100000
// 			val = strings.TrimSuffix(val, "L")
// 		}
// 		if f, err := strconv.ParseFloat(val, 64); err == nil {
// 			return f * multiplier
// 		}
// 		return 0
// 	}
// 	return 0
// }

// // ============================================================
// // normalizeParameters
// // ============================================================

// func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
// 	params.Industry = strings.TrimSpace(params.Industry)
// 	params.Category = strings.TrimSpace(params.Category)
// 	params.Subcategory = strings.TrimSpace(params.Subcategory)

// 	if params.Location != nil {
// 		if params.Location.Country == "" {
// 			params.Location.Country = "India"
// 		}
// 		params.Location.City = strings.TrimSpace(params.Location.City)
// 		if len(params.Location.City) > 0 {
// 			params.Location.City = strings.ToUpper(params.Location.City[:1]) +
// 				strings.ToLower(params.Location.City[1:])
// 		}
// 	}

// 	if params.Investment != nil {
// 		if params.Investment.Min < 0 {
// 			params.Investment.Min = 0
// 		}
// 		if params.Investment.Max < 0 {
// 			params.Investment.Max = 0
// 		}
// 		if params.Investment.Min > params.Investment.Max && params.Investment.Max > 0 {
// 			params.Investment.Min, params.Investment.Max =
// 				params.Investment.Max, params.Investment.Min
// 		}
// 	}

// 	if params.ROI != nil {
// 		if params.ROI.Max > 100 {
// 			params.ROI.Max = 100
// 		}
// 		if params.ROI.Min < 0 {
// 			params.ROI.Min = 0
// 		}
// 		if params.ROI.Min > params.ROI.Max {
// 			params.ROI.Min, params.ROI.Max = params.ROI.Max, params.ROI.Min
// 		}
// 	}

// 	if params.Rating != nil {
// 		if *params.Rating < 0 {
// 			z := 0.0
// 			params.Rating = &z
// 		}
// 		if *params.Rating > 5 {
// 			f := 5.0
// 			params.Rating = &f
// 		}
// 	}

// 	if params.Space != nil {
// 		if params.Space.Min < 0 {
// 			params.Space.Min = 0
// 		}
// 		if params.Space.Min > params.Space.Max {
// 			params.Space.Min, params.Space.Max = params.Space.Max, params.Space.Min
// 		}
// 	}

// 	if params.Staff != nil {
// 		if params.Staff.Min < 0 {
// 			params.Staff.Min = 0
// 		}
// 		if params.Staff.Min > params.Staff.Max {
// 			params.Staff.Min, params.Staff.Max = params.Staff.Max, params.Staff.Min
// 		}
// 	}

// 	if params.Outlets != nil && *params.Outlets < 0 {
// 		z := 0
// 		params.Outlets = &z
// 	}

// 	return nil
//  }

/////////////////////////////////////////////////////////////////////////

// package ai_search

// import (
// 	"encoding/json"
// 	"fmt"
// 	"strconv"
// 	"strings"
// )

// type ParameterExtractor struct {
// 	config *Config
// }

// func NewParameterExtractor(config *Config) *ParameterExtractor {
// 	return &ParameterExtractor{config: config}
// }

// // BuildPrompt - Modelfile mein TEMPLATE + SYSTEM already set hai
// // Ollama automatically ChatML wrap karta hai jab /api/generate call hoti hai
// // Isliye sirf plain query bhejna hai — server.py bhi yahi karta tha internally
// //
// //	func (pe *ParameterExtractor) BuildPrompt(query string) string {
// //		return query
// //	}
// func (pe *ParameterExtractor) BuildPrompt(query string) string {
// 	q := strings.TrimSpace(query)
// 	// Single word hai toh franchise context add karo
// 	if len(strings.Fields(q)) == 1 {
// 		return q + " franchise"
// 	}
// 	return q
// }

// // ftModelOutput - Fine-tuned model ka exact output schema (notebook se)
// type ftModelOutput struct {
// 	Error             interface{} `json:"error"`
// 	Industry          interface{} `json:"Industry"`
// 	Category          interface{} `json:"Category"`
// 	Subcategory       interface{} `json:"Subcategory"`
// 	Location          interface{} `json:"Location"`
// 	MinimumInvestment interface{} `json:"Minimum_Investment"`
// 	MaximumInvestment interface{} `json:"Maximum_Investment"`
// 	AreaRequirement   interface{} `json:"Area_Requirement"`
// 	ROI               interface{} `json:"ROI"`
// }

// // Parse - Fine-tuned model ke flat schema ko Go ke ExtractedParameters mein convert karta hai
// func (pe *ParameterExtractor) Parse(llmResponse string) (*ExtractedParameters, error) {

// 	// ✅ ADD THIS — raw LLM response dekho
// 	fmt.Printf("🔍 RAW LLM RESPONSE: %s\n", llmResponse)

// 	// Step 1: Clean markdown artifacts
// 	cleaned := strings.TrimSpace(llmResponse)
// 	cleaned = strings.TrimPrefix(cleaned, "```json")
// 	cleaned = strings.TrimPrefix(cleaned, "```")
// 	cleaned = strings.TrimSuffix(cleaned, "```")
// 	cleaned = strings.TrimSpace(cleaned)

// 	// Step 2: Extract JSON object
// 	jsonStart := strings.Index(cleaned, "{")
// 	jsonEnd := strings.LastIndex(cleaned, "}")
// 	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
// 		return nil, fmt.Errorf("no valid JSON found in: %s", cleaned)
// 	}
// 	cleaned = cleaned[jsonStart : jsonEnd+1]

// 	// Step 3: Parse into fine-tuned model's schema
// 	var ftOut ftModelOutput
// 	if err := json.Unmarshal([]byte(cleaned), &ftOut); err != nil {
// 		return nil, fmt.Errorf("JSON parse failed: %w, raw: %s", err, cleaned)
// 	}

// 	// Step 4: Check for OOD (out of domain)
// 	if errVal, ok := ftOut.Error.(string); ok && strings.Contains(strings.ToLower(errVal), "out of domain") {
// 		return &ExtractedParameters{}, nil // Empty params = no search refinement
// 	}

// 	// Step 5: Map flat schema → ExtractedParameters
// 	params := &ExtractedParameters{}

// 	// Industry
// 	// if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
// 	// 	params.Industry = strings.TrimSpace(v)
// 	// }
// 	if v, ok := ftOut.Industry.(string); ok && strings.TrimSpace(v) != "" {
// 		category := ""
// 		if c, ok := ftOut.Category.(string); ok {
// 			category = c
// 		}
// 		params.Industry = normalizeIndustry(strings.TrimSpace(v), category)
// 	}

// 	// Category
// 	if v, ok := ftOut.Category.(string); ok && strings.TrimSpace(v) != "" {
// 		params.Category = strings.TrimSpace(v)
// 	}

// 	// Subcategory
// 	if v, ok := ftOut.Subcategory.(string); ok && strings.TrimSpace(v) != "" {
// 		params.Subcategory = strings.TrimSpace(v)
// 	}

// 	// Location - notebook mein string hai (city name directly), Go mein LocationFilter struct
// 	if v, ok := ftOut.Location.(string); ok && strings.TrimSpace(v) != "" {
// 		city := strings.TrimSpace(v)
// 		// Title case normalize
// 		city = strings.ToUpper(city[:1]) + strings.ToLower(city[1:])
// 		params.Location = &LocationFilter{
// 			City:    city,
// 			Country: "India",
// 		}
// 	}

// 	// Investment - notebook mein Minimum_Investment aur Maximum_Investment alag fields hain
// 	minInv := toFloat64(ftOut.MinimumInvestment)
// 	maxInv := toFloat64(ftOut.MaximumInvestment)
// 	if minInv > 0 || maxInv > 0 {
// 		inv := &InvestmentFilter{}
// 		if minInv > 0 {
// 			inv.Min = minInv
// 		}
// 		if maxInv > 0 {
// 			inv.Max = maxInv
// 		}
// 		// Agar sirf max diya hai toh min = max/10
// 		if inv.Min == 0 && inv.Max > 0 {
// 			inv.Min = inv.Max / 10
// 		}
// 		// Agar sirf min diya hai toh max = min * 5
// 		if inv.Max == 0 && inv.Min > 0 {
// 			inv.Max = inv.Min * 5
// 		}
// 		params.Investment = inv
// 	}

// 	// Area_Requirement → Space field
// 	areaVal := toFloat64(ftOut.AreaRequirement)
// 	if areaVal > 0 {
// 		params.Space = &RangeFilter{
// 			Min: areaVal * 0.8, // ±20% range
// 			Max: areaVal * 1.5,
// 		}
// 	}

// 	// ROI
// 	roiVal := toFloat64(ftOut.ROI)
// 	if roiVal > 0 {
// 		if roiVal > 100 {
// 			roiVal = 100
// 		}
// 		params.ROI = &RangeFilter{
// 			Min: roiVal,
// 			Max: roiVal + 10,
// 		}
// 	}

// 	// Normalize
// 	if err := pe.normalizeParameters(params); err != nil {
// 		return nil, fmt.Errorf("normalization failed: %w", err)
// 	}

// 	return params, nil
// }

// var industryNormalizationMap = map[string]string{
// 	"automotive":                       "Automotive",
// 	"beauty":                           "Beauty",
// 	"health":                           "Health",
// 	"food & beverage":                  "Food & Beverage",
// 	"home-based business":              "Home-Based Business",
// 	"retail":                           "Retail",
// 	"education":                        "Education",
// 	"fashion":                          "Fashion",
// 	"entertainment":                    "Entertainment",
// 	"business & professional services": "Business Services",
// 	"education & edtech":               "Education",
// 	// "health & fitness":                 "Health",
// 	"entertainment & leisure":          "Entertainment",
// 	"real estate & property services":  "Real Estate",
// 	"financial services":               "Finance / Banking",
// 	"logistics & delivery services":    "Logistics / Manufacturing",
// 	"agriculture & sustainability":     "Agriculture",
// 	"hospitality & lodging":            "Hotel, Travel & Tourism",
// 	"hospitality":                      "Hotel, Travel & Tourism",
// 	"automobile services":              "Automotive",
// 	"beauty, personal care & grooming": "Beauty",
// 	"home services":                    "Home-Based Business",
// }

// func normalizeIndustry(industry string, category string) string {
// 	lower := strings.ToLower(strings.TrimSpace(industry))
// 	catLower := strings.ToLower(strings.TrimSpace(category))

// 	// ✅ Travel check PEHLE — map se pehle (government + travel category)
// 	travelKeywords := []string{"resort", "holiday", "tourism", "travel", "hotel", "lodge", "guesthouse", "destination"}

// 	if lower == "government" {
// 		for _, kw := range travelKeywords {
// 			if strings.Contains(catLower, kw) {
// 				return "Hotel, Travel & Tourism"
// 			}
// 		}
// 		return "Government"
// 	}

// 	if lower == "business services" || lower == "business & professional services" {
// 		for _, kw := range travelKeywords {
// 			if strings.Contains(catLower, kw) {
// 				return "Hotel, Travel & Tourism"
// 			}
// 		}
// 		if strings.Contains(catLower, "dealer") || strings.Contains(catLower, "distributor") {
// 			return "Dealers & Distributors"
// 		}
// 		if strings.Contains(catLower, "software") || strings.Contains(catLower, "it service") || strings.Contains(catLower, "tech") {
// 			return "Technology / IT"
// 		}
// 		if strings.Contains(catLower, "advertis") || strings.Contains(catLower, "media") {
// 			return "Media / Communication"
// 		}
// 		return "Business Services"
// 	}

// 	if lower == "retail" {
// 		if strings.Contains(catLower, "fashion") || strings.Contains(catLower, "apparel") || strings.Contains(catLower, "clothing") {
// 			return "Fashion"
// 		}
// 		return "Retail"
// 	}

// 	if lower == "health & fitness" {
// 		fitnessKeywords := []string{
// 			"gym", "yoga", "pilates", "fitness centre",
// 			"crossfit", "zumba", "aerobics", "sports",
// 			"swimming", "martial arts", "boxing",
// 		}
// 		for _, kw := range fitnessKeywords {
// 			if strings.Contains(catLower, kw) {
// 				return "Sports & Fitness"
// 			}
// 		}
// 		// category match nahi hua toh Health
// 		return "Health"
// 	}

// 	if normalized, ok := industryNormalizationMap[lower]; ok {
// 		return normalized
// 	}
// 	return industry
// }

// func toFloat64(v interface{}) float64 {
// 	if v == nil {
// 		return 0
// 	}
// 	switch val := v.(type) {
// 	case float64:
// 		return val
// 	case int:
// 		return float64(val)
// 	case int64:
// 		return float64(val)
// 	case string:
// 		val = strings.TrimSpace(strings.ToUpper(val))
// 		if val == "" || val == "NULL" {
// 			return 0
// 		}
// 		// "20L" → 2000000, "1.5CR" → 15000000
// 		multiplier := 1.0
// 		if strings.HasSuffix(val, "CR") {
// 			multiplier = 10000000
// 			val = strings.TrimSuffix(val, "CR")
// 		} else if strings.HasSuffix(val, "L") {
// 			multiplier = 100000
// 			val = strings.TrimSuffix(val, "L")
// 		}
// 		if f, err := strconv.ParseFloat(val, 64); err == nil {
// 			return f * multiplier
// 		}
// 		return 0
// 	}
// 	return 0
// }

// // normalizeParameters - validates and normalizes all parameters
// func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
// 	if params.Industry != "" {
// 		params.Industry = strings.TrimSpace(params.Industry)
// 	}
// 	if params.Category != "" {
// 		params.Category = strings.TrimSpace(params.Category)
// 	}
// 	if params.Subcategory != "" {
// 		params.Subcategory = strings.TrimSpace(params.Subcategory)
// 	}

// 	if params.Location != nil && params.Location.City != "" {
// 		if params.Location.Country == "" {
// 			params.Location.Country = "India"
// 		}
// 		params.Location.City = strings.TrimSpace(params.Location.City)
// 		if len(params.Location.City) > 0 {
// 			params.Location.City = strings.ToUpper(params.Location.City[:1]) + strings.ToLower(params.Location.City[1:])
// 		}
// 	}

// 	if params.Investment != nil {
// 		if params.Investment.Min < 0 {
// 			params.Investment.Min = 0
// 		}
// 		if params.Investment.Max < 0 {
// 			params.Investment.Max = 0
// 		}
// 		if params.Investment.Min > params.Investment.Max && params.Investment.Max > 0 {
// 			params.Investment.Min, params.Investment.Max = params.Investment.Max, params.Investment.Min
// 		}
// 	}

// 	if params.ROI != nil {
// 		if params.ROI.Max > 100 {
// 			params.ROI.Max = 100
// 		}
// 		if params.ROI.Min < 0 {
// 			params.ROI.Min = 0
// 		}
// 		if params.ROI.Min > params.ROI.Max {
// 			params.ROI.Min, params.ROI.Max = params.ROI.Max, params.ROI.Min
// 		}
// 	}

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

// 	if params.Space != nil {
// 		if params.Space.Min < 0 {
// 			params.Space.Min = 0
// 		}
// 		if params.Space.Min > params.Space.Max {
// 			params.Space.Min, params.Space.Max = params.Space.Max, params.Space.Min
// 		}
// 	}

// 	if params.Staff != nil {
// 		if params.Staff.Min < 0 {
// 			params.Staff.Min = 0
// 		}
// 		if params.Staff.Min > params.Staff.Max {
// 			params.Staff.Min, params.Staff.Max = params.Staff.Max, params.Staff.Min
// 		}
// 	}

// 	if params.Outlets != nil && *params.Outlets < 0 {
// 		zero := 0
// 		params.Outlets = &zero
// 	}

// 	return nil
// }

// // ParseWithFallback - parse karo, failure pe empty params return karo
// func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {

// 	// ✅ ADD THIS
// 	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)

// 	params, err := pe.Parse(llmResponse)
// 	if err != nil {
// 		return &ExtractedParameters{}
// 	}
// 	return params
// }
