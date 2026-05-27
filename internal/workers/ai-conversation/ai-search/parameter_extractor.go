package ai_search

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"camunda-workers/internal/common/location"
)

type ParameterExtractor struct {
	config       *Config
	cityRegexes  map[string]*regexp.Regexp
	sortedZones  []string
	sortedStates []stateMapEntry
}

type stateMapEntry struct {
	lower  string
	proper string
}

func NewParameterExtractor(config *Config) *ParameterExtractor {
	pe := &ParameterExtractor{
		config:      config,
		cityRegexes: make(map[string]*regexp.Regexp),
	}

	// 1. Pre-compile city regexes
	for _, city := range getAllKnownCities() {
		cityLower := strings.ToLower(city)
		re := regexp.MustCompile("(?i)\\b" + regexp.QuoteMeta(cityLower) + "\\b")
		pe.cityRegexes[cityLower] = re
	}

	// 2. Pre-sort zones (Longest First)
	for zone := range zoneNames {
		pe.sortedZones = append(pe.sortedZones, zone)
	}
	sort.Slice(pe.sortedZones, func(i, j int) bool {
		return len(pe.sortedZones[i]) > len(pe.sortedZones[j])
	})

	// 3. Pre-sort states (Longest First)
	for lower, proper := range stateNames {
		pe.sortedStates = append(pe.sortedStates, stateMapEntry{lower: lower, proper: proper})
	}
	sort.Slice(pe.sortedStates, func(i, j int) bool {
		return len(pe.sortedStates[i].lower) > len(pe.sortedStates[j].lower)
	})

	return pe
}

func (pe *ParameterExtractor) BuildPrompt(query string) string {
	q := strings.TrimSpace(query)
	if len(strings.Fields(q)) == 1 {
		q = q + " franchise"
	}

	return `You are a highly accurate entity extraction AI for a franchise search engine. Extract search parameters from the user's query and output them EXACTLY in the specified JSON format.

TAXONOMY:
Industry → Category → Subcategory

RULES:
1. Return ONLY valid JSON. No markdown, no conversational text.
2. If a value is missing, use null. DO NOT use empty strings.
3. For investments, standardize Indian currency: convert "1 lakh", "10 lacs" to "1L", "10L". Convert "1 crore", "2 cr" to "1Cr", "2Cr".
4. Determine Min/Max Investment carefully: 
   - "under", "below", "budget of", "max" -> Maximum_Investment
   - "above", "starting from", "min" -> Minimum_Investment
   - "between X to Y" -> Minimum_Investment = X, Maximum_Investment = Y
5. Area_Requirement should be in numbers (sq ft).
6. ROI should be just the percentage number (e.g., 20).
7. Rating should be a number between 0 and 5.
8. Only set Verified or Trusted_Seller to true if the words "verified" or "trusted" are explicitly used in the user's query. Otherwise, they must be null.

DATA STRUCTURE (Return ONLY valid JSON matching this):
{
  "Industry": "string or null",
  "Category": "string or null",
  "Subcategory": "string or null",
  "Location": "string (City/State) or null",
  "Minimum_Investment": "string (e.g., '1L', '50L', '1Cr') or null",
  "Maximum_Investment": "string (e.g., '10L', '2Cr') or null",
  "Area_Requirement": "string (e.g., '500') or null",
  "ROI": "string (e.g., '20') or null",
  "Rating": "number or null",
  "Staff": "number or null",
  "Outlets": "number or null",
  "Verified": "boolean or null",
  "Trusted_Seller": "boolean or null"
}

EXAMPLES:
Query: "food franchise under 1 lakh in delhi with high rating"
Output: {"Industry": "Food & Beverage", "Category": null, "Subcategory": null, "Location": "Delhi", "Minimum_Investment": null, "Maximum_Investment": "1L", "Area_Requirement": null, "ROI": null, "Rating": 4.5, "Staff": null, "Outlets": null, "Verified": null, "Trusted_Seller": null}

Query: "pizza business in mumbai"
Output: {"Industry": "Food & Beverage", "Category": "Pizza", "Subcategory": null, "Location": "Mumbai", "Minimum_Investment": null, "Maximum_Investment": null, "Area_Requirement": null, "ROI": null, "Rating": null, "Staff": null, "Outlets": null, "Verified": null, "Trusted_Seller": null}

Query: "verified education business between 10 to 20 lakh"
Output: {"Industry": "Education", "Category": null, "Subcategory": null, "Location": null, "Minimum_Investment": "10L", "Maximum_Investment": "20L", "Area_Requirement": null, "ROI": null, "Rating": null, "Staff": null, "Outlets": null, "Verified": true, "Trusted_Seller": null}

Query: "` + q + `"
Output:`
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
	Rating            interface{} `json:"Rating"`
	Staff             interface{} `json:"Staff"`
	Outlets           interface{} `json:"Outlets"`
	Verified          interface{} `json:"Verified"`
	TrustedSeller     interface{} `json:"Trusted_Seller"`
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
	"food":                             "Food & Beverage",
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

func getSlugFallback(industryName string) string {
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

// GetIndustrySlug - direct slug lookup, supporting comma-separated multiple industry names
func GetIndustrySlug(industryName string) string {
	if strings.Contains(industryName, ",") {
		parts := strings.Split(industryName, ",")
		var slugs []string
		for _, part := range parts {
			partTrimmed := strings.TrimSpace(part)
			if partTrimmed == "" {
				continue
			}
			if slug, ok := industrySlugMap[partTrimmed]; ok {
				slugs = append(slugs, slug)
			} else {
				slugs = append(slugs, getSlugFallback(partTrimmed))
			}
		}
		if len(slugs) > 0 {
			return strings.Join(slugs, ",")
		}
	}

	if slug, ok := industrySlugMap[industryName]; ok {
		return slug
	}
	return getSlugFallback(industryName)
}

func normalizeIndustry(industry string, category string) string {
	lower := strings.ToLower(strings.TrimSpace(industry))
	catLower := strings.ToLower(strings.TrimSpace(category))

	// 1. If category itself is a known top-level industry, use that!
	// This handles cases where LLM says Industry: "Retail", Category: "Food & Beverage"
	// but in our DB "Food & Beverage" is a top-level industry.
	if normalized, ok := industryNormalizationMap[catLower]; ok {
		return normalized
	}

	// 2. Health & Fitness — category se decide karo
	if lower == "health & fitness" || lower == "health" || lower == "fitness" {
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
		if lower == "health" {
			return "Health"
		}
		return "Health" // Default for "health & fitness" if no fitness kws
	}

	// 3. Retail → Apparel/Fashion Retail → Fashion industry
	if lower == "retail" {
		if strings.Contains(catLower, "apparel") ||
			strings.Contains(catLower, "fashion") ||
			strings.Contains(catLower, "clothing") {
			return "Fashion"
		}
		return "Retail"
	}

	// 4. Business Services — subcategory check
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

// hqKeywords and isHQOnlyLocation removed (duplicate at line 356)

// ============================================================
// DUAL LOCATION DETECTION (Robust Regex Fix)
// ============================================================

// matchCityWithBoundaries — fixes "Ranchi inside franchise" bug
func (pe *ParameterExtractor) matchCityWithBoundaries(query, city string) bool {
	cityLower := strings.ToLower(city)
	if re, ok := pe.cityRegexes[cityLower]; ok {
		return re.MatchString(strings.ToLower(query))
	}
	// Fallback for names not in pre-compiled list
	re := regexp.MustCompile("(?i)\\b" + regexp.QuoteMeta(cityLower) + "\\b")
	return re.MatchString(strings.ToLower(query))
}

var (
	// Target signals: where they WANT
	targetLocMarkers = `(mein\s+franchise\s+lena|mein\s+lena|mein\s+open|mein\s+start|mein\s+business|mein\s+setup|in|at|opening\s+in|want\s+to\s+open\s+in|seeking\s+in|searching\s+in|looking\s+in|for|setup\s+in|expansion\s+in)`
	targetLocRegex   = regexp.MustCompile(`(?i)\b` + targetLocMarkers + `\b`)
	// User signals: where they ARE
	userLocMarkers = `(mujhe|mera|currently\s+in|based\s+in|hailing\s+from|from|live\s+in|stay\s+in|staying\s+in|living\s+in|residing\s+in|based\s+out\s+of)`
	userLocRegex   = regexp.MustCompile(`(?i)\b` + userLocMarkers + `\b`)

	// Investment Regexes
	// Pattern: [under/below/...] [number] [L/Lakh/Cr/...]
	maxInvRegex = regexp.MustCompile(`(?i)\b(under|below|less\s+than|upto|within|budget\s+(?:of|is)|limit\s+(?:of|is))\s*([0-9.]+)\s*(lakhs?|l|cr|crores?)\b`)
	// Pattern: [above/starting/...] [number] [L/Lakh/Cr/...]
	minInvRegex = regexp.MustCompile(`(?i)\b(above|more\s+than|greater\s+than|starting\s+from|from|starts?\s+at|at\s+least|min|minimum)\s*([0-9.]+)\s*(lakhs?|l|cr|crores?)\b`)
	// Pattern: [number] [L/Lakh/Cr/...] [to/-] [number] [L/Lakh/Cr/...]
	rangeInvRegex = regexp.MustCompile(`(?i)\b([0-9.]+)\s*(lakhs?|l|cr|crores?)?\s*(to|and|-)\s*([0-9.]+)\s*(lakhs?|l|cr|crores?)\b`)
)

func (pe *ParameterExtractor) extractTargetCity(query string, skipCity string) string {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	
	candidates := location.DetectAllCitiesFromQuery(queryLower)
	if len(candidates) == 0 {
		return ""
	}

	// Priority 1: Candidate with target marker nearby
	for _, c := range candidates {
		cLower := strings.ToLower(c)
		if cLower == strings.ToLower(skipCity) || isHQOnlyLocation(queryLower, cLower) {
			continue
		}

		// Double Check: is it actually the user location?
		if pe.isCityUserLocation(queryLower, cLower) {
			continue
		}

		// Check for Target Marker nearby
		cityIdx := strings.Index(queryLower, cLower)
		targetIdx := targetLocRegex.FindAllStringIndex(queryLower, -1)
		for _, idxRange := range targetIdx {
			// Marker within range?
			if (idxRange[1] < cityIdx && (cityIdx-idxRange[1]) < 25) || (idxRange[0] > cityIdx && (idxRange[0]-cityIdx-len(cLower)) < 25) {
				return c
			}
		}
	}

	// Priority 2: Generic candidate (not User or HQ)
	for _, c := range candidates {
		cLower := strings.ToLower(c)
		if cLower == strings.ToLower(skipCity) || isHQOnlyLocation(queryLower, cLower) {
			continue
		}
		if !pe.isCityUserLocation(queryLower, cLower) {
			return c
		}
	}

	return ""
}

func (pe *ParameterExtractor) extractAllTargetCities(query string, skipCity string) []string {
	queryLower := strings.ToLower(strings.TrimSpace(query))
	
	candidates := location.DetectAllCitiesFromQuery(queryLower)
	if len(candidates) == 0 {
		return nil
	}

	var results []string
	for _, c := range candidates {
		cLower := strings.ToLower(c)
		if cLower == strings.ToLower(skipCity) || isHQOnlyLocation(queryLower, cLower) {
			continue
		}
		if !pe.isCityUserLocation(queryLower, cLower) {
			results = append(results, c)
		}
	}

	return results
}

func isHQOnlyLocation(queryLower, cityLower string) bool {
	// Markers that indicate the city is the BRAND'S location, not the USER'S target
	hqMarkers := []string{
		"head office", "headquarters", "hq", "corporate office", "parent company",
		"based out of", "based in", "headquartered", "unka office", "brand ka office",
		"company is from", "brand is based", "office is in", "office is located",
		"main branch",
	}

	for _, marker := range hqMarkers {
		// Pattern: [marker] ... up to 25 chars ... [city]
		// Allows "head office is in...", "headquarters relocated to...", "hq based out of..."
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(marker) + `[\s\w]{0,25}\b` + regexp.QuoteMeta(cityLower) + `\b`)
		if re.MatchString(queryLower) {
			return true
		}
	}
	return false
}

func (pe *ParameterExtractor) isCityUserLocation(queryLower, cityLower string) bool {
	// Pattern: User marker followed by [City]
	markers := []string{
		"currently in", "based in", "hailing from", "live in", "stay in", "staying in",
		"living in", "residing in", "based out of", "from", "mein hun", "se hun",
		"mujhe", "mera base", "mera location",
	}

	foundMarker := false
	var userMatch []int
	for _, m := range markers {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(m) + `\s+` + regexp.QuoteMeta(cityLower) + `\b`)
		if re.MatchString(queryLower) {
			userMatch = re.FindStringIndex(queryLower)
			foundMarker = true
			break
		}
	}

	if foundMarker {
		// Priority check: if OTHER target markers are present and closer, it's a target
		cityIdx := strings.Index(queryLower, cityLower)
		targetIdx := targetLocRegex.FindAllStringIndex(queryLower, -1)

		for _, idxRange := range targetIdx {
			if idxRange[1] < cityIdx && (cityIdx-idxRange[1]) < 15 {
				if idxRange[0] >= userMatch[0] && idxRange[1] <= userMatch[1] {
					continue // Ignore 'in' from 'based in'
				}
				return false // It's likely a target
			}
		}
		return true
	}

	// Hinglish "Noida mein hun" or "Main Noida se hun"
	reSuffix := regexp.MustCompile("(?i)\\b" + regexp.QuoteMeta(cityLower) + "\\s+(mein\\s+hun|se\\s+hun|mein\\s+rehta\\s+hun)")
	if reSuffix.MatchString(queryLower) {
		return true
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

	// Sort by length descending to ensure deterministic and zero-miss longest-match-first
	sort.Slice(cities, func(i, j int) bool {
		return len(cities[i]) > len(cities[j])
	})

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

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

func (pe *ParameterExtractor) parseLocationString(locStr string) *LocationFilter {
	locLower := strings.ToLower(strings.TrimSpace(locStr))

	if locLower == "" || locLower == "null" || locLower == "none" || locLower == "undefined" {
		return nil
	}

	for _, zone := range pe.sortedZones {
		if locLower == zone {
			// Title case every word (e.g., "northeast india" -> "Northeast India")
			return &LocationFilter{City: titleCase(zone), Country: "India"}
		}
	}

	for _, entry := range pe.sortedStates {
		if locLower == entry.lower {
			return &LocationFilter{City: entry.proper, State: entry.proper, Country: "India"}
		}
	}

	cityProper := titleCase(locStr)
	// Normalization for cities (e.g., Bangalore -> Bengaluru)
	for proper, aliases := range location.CityAliases {
		for _, alias := range aliases {
			if strings.ToLower(alias) == locLower {
				cityProper = strings.ToUpper(proper[:1]) + strings.ToLower(proper[1:])
				break
			}
		}
	}

	return &LocationFilter{City: cityProper, Country: "India"}
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

	ratingVal := toFloat64(ftOut.Rating)
	if ratingVal > 0 {
		params.Rating = &ratingVal
	}

	staffVal := toFloat64(ftOut.Staff)
	if staffVal > 0 {
		params.Staff = &RangeFilter{Min: staffVal, Max: staffVal * 3}
	}

	outletsVal := int(toFloat64(ftOut.Outlets))
	if outletsVal > 0 {
		params.Outlets = &outletsVal
	}

	if v, ok := ftOut.Verified.(bool); ok && v {
		params.Verified = &v
	} else if vStr, ok := ftOut.Verified.(string); ok && strings.ToLower(vStr) == "true" {
		t := true
		params.Verified = &t
	}

	if v, ok := ftOut.TrustedSeller.(bool); ok && v {
		params.TrustedSeller = &v
	} else if vStr, ok := ftOut.TrustedSeller.(string); ok && strings.ToLower(vStr) == "true" {
		t := true
		params.TrustedSeller = &t
	}

	if err := pe.normalizeParameters(params); err != nil {
		return nil, fmt.Errorf("normalization failed: %w", err)
	}

	return params, nil
}

// ============================================================
// ParseWithContext — location fix with original query
// ============================================================

func (pe *ParameterExtractor) ParseWithContext(llmResponse string, originalQuery string) *ExtractedParameters {
	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)

	params, err := pe.Parse(llmResponse)
	if err != nil {
		fmt.Printf("⚠️  Parse error: %v\n", err)
		return &ExtractedParameters{OriginalQuery: originalQuery}
	}
	params.OriginalQuery = originalQuery

	if originalQuery == "" {
		return params
	}

	queryLower := strings.ToLower(originalQuery)

	// FIX 0: Investment Fallback from query
	if params.Investment == nil {
		inv := pe.extractInvestmentFromQuery(queryLower)
		if inv != nil {
			params.Investment = inv
			fmt.Printf("💰 Investment from query: Min=%v, Max=%v\n", inv.Min, inv.Max)
		}
	}

	// Always scan original query for all mentioned industries to support multi-industry search
	var allIndustries []string
	seenInd := make(map[string]bool)

	// Add LLM industry if present
	if params.Industry != "" {
		if !seenInd[params.Industry] {
			seenInd[params.Industry] = true
			allIndustries = append(allIndustries, params.Industry)
		}
	}

	// Scan query and match all industries
	// Sort keywords by length descending to match longest first
	var kws []string
	for kw := range industryNormalizationMap {
		kws = append(kws, kw)
	}
	sort.Slice(kws, func(i, j int) bool {
		return len(kws[i]) > len(kws[j])
	})

	for _, kw := range kws {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
		if re.MatchString(queryLower) {
			normalized := industryNormalizationMap[kw]
			if !seenInd[normalized] {
				seenInd[normalized] = true
				allIndustries = append(allIndustries, normalized)
			}
		}
	}

	if len(allIndustries) > 0 {
		params.Industry = strings.Join(allIndustries, ", ")
		fmt.Printf("🏭 Merged Industries: %s\n", params.Industry)
	}

	// FIX 1: HQ Location
	if params.Location != nil && params.Location.City != "" {
		if isHQOnlyLocation(originalQuery, params.Location.City) {
			targetCity := pe.extractTargetCity(originalQuery, params.Location.City)
			if targetCity != "" {
				fmt.Printf("🏢 HQ fix: '%s' → '%s'\n", params.Location.City, targetCity)
				params.Location = pe.parseLocationString(targetCity)
			} else {
				fmt.Printf("🏢 HQ location cleared\n")
				params.Location = nil
			}
		}
	}

	// FIX 2: Dual Location - Model correctly output 'Noida' but query wants 'Chennai'
	if params.Location != nil && params.Location.City != "" {
		cityLower := strings.ToLower(params.Location.City)
		if pe.isCityUserLocation(queryLower, cityLower) {
			fmt.Printf("🕵️  User location detected: %s. Searching for target...\n", params.Location.City)
			targetCity := pe.extractTargetCity(originalQuery, params.Location.City)
			if targetCity != "" && strings.ToLower(targetCity) != cityLower {
				fmt.Printf("📍 Dual location fix: '%s' → '%s'\n", params.Location.City, targetCity)
				params.Location = pe.parseLocationString(targetCity)
			}
		}
	}

	// FIX 3: Zone/State/City Fallback from query (Deterministic Order)
	if params.Location == nil || params.Location.City == "" {
		var foundLocations []string

		// 1. Check for Zones
		for _, zone := range pe.sortedZones {
			if strings.Contains(queryLower, zone) {
				if !pe.isCityUserLocation(queryLower, zone) {
					foundLocations = append(foundLocations, titleCase(zone))
				}
			}
		}

		// 2. Check for States
		for _, entry := range pe.sortedStates {
			if strings.Contains(queryLower, entry.lower) {
				if !pe.isCityUserLocation(queryLower, entry.lower) {
					foundLocations = append(foundLocations, entry.proper)
				}
			}
		}

		// 3. Check for Cities
		targetCities := pe.extractAllTargetCities(originalQuery, "")
		for _, city := range targetCities {
			foundLocations = append(foundLocations, city)
		}

		if len(foundLocations) > 0 {
			// Dedup foundLocations
			seen := make(map[string]bool)
			var uniqueLocations []string
			for _, loc := range foundLocations {
				locTitled := titleCase(loc)
				// E.g. "Delhi NCR" and "Delhi" are same, let's treat "Delhi NCR" as standard
				if locTitled == "Delhi" {
					locTitled = "Delhi NCR"
				}
				if !seen[strings.ToLower(locTitled)] {
					seen[strings.ToLower(locTitled)] = true
					uniqueLocations = append(uniqueLocations, locTitled)
				}
			}

			if len(uniqueLocations) > 0 {
				joinedCities := strings.Join(uniqueLocations, ", ")
				params.Location = &LocationFilter{
					City:    joinedCities,
					Country: "India",
				}
				fmt.Printf("🗺️  Extracted multiple locations from query: %s\n", joinedCities)
			}
		}
	}

	return params
}

func (pe *ParameterExtractor) extractInvestmentFromQuery(query string) *InvestmentFilter {
	// 1. Range Check (e.g., "5 to 10 lakh")
	if matches := rangeInvRegex.FindStringSubmatch(query); len(matches) >= 6 {
		val1 := parseNumericValue(matches[1], matches[2])
		val2 := parseNumericValue(matches[4], matches[5])
		if val1 == 0 && matches[2] == "" && matches[5] != "" {
			// Handles "5 to 10 lakh" where first unit is missing
			val1 = parseNumericValue(matches[1], matches[5])
		}
		if val1 > 0 && val2 > 0 {
			if val1 > val2 {
				val1, val2 = val2, val1
			}
			return &InvestmentFilter{Min: val1, Max: val2}
		}
	}

	// 2. Max Check (e.g., "under 10 lakh")
	if matches := maxInvRegex.FindStringSubmatch(query); len(matches) >= 4 {
		val := parseNumericValue(matches[2], matches[3])
		if val > 0 {
			return &InvestmentFilter{Min: val / 10, Max: val}
		}
	}

	// 3. Min Check (e.g., "above 5 lakh")
	if matches := minInvRegex.FindStringSubmatch(query); len(matches) >= 4 {
		val := parseNumericValue(matches[2], matches[3])
		if val > 0 {
			return &InvestmentFilter{Min: val, Max: val * 5}
		}
	}

	// 4. Generic Check (e.g., "10 lakh budget")
	genericRegex := regexp.MustCompile(`(?i)\b([0-9.]+)\s*(lakhs?|l|cr|crores?)\b`)
	if matches := genericRegex.FindStringSubmatch(query); len(matches) >= 3 {
		val := parseNumericValue(matches[1], matches[2])
		if val > 0 {
			return &InvestmentFilter{Min: val * 0.5, Max: val * 1.5}
		}
	}

	return nil
}

func parseNumericValue(valStr, unitStr string) float64 {
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0
	}

	unit := strings.ToLower(unitStr)
	multiplier := 1.0
	if strings.HasPrefix(unit, "l") {
		multiplier = 100000
	} else if strings.HasPrefix(unit, "c") {
		multiplier = 10000000
	}

	return val * multiplier
}

func (pe *ParameterExtractor) ParseWithFallback(llmResponse string) *ExtractedParameters {
	fmt.Printf("🔍 RAW LLM: [%s]\n", llmResponse)
	params, err := pe.Parse(llmResponse)
	if err != nil {
		return &ExtractedParameters{}
	}
	return params
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
		// Strip percentage and other noise
		val = strings.ReplaceAll(val, "%", "")
		val = strings.ReplaceAll(val, " PERCENT", "")
		val = strings.ReplaceAll(val, " ABOVE", "")
		val = strings.ReplaceAll(val, " UNDER", "")
		val = strings.ReplaceAll(val, "AT LEAST", "")
		val = strings.ReplaceAll(val, "MINIMUM", "")
		val = strings.TrimSpace(val)

		multiplier := 1.0
		valUpper := strings.ToUpper(val)
		if strings.Contains(valUpper, "CR") {
			multiplier = 10000000
			val = strings.TrimSpace(strings.ReplaceAll(valUpper, "CR", ""))
		} else if strings.Contains(valUpper, "L") {
			multiplier = 100000
			val = strings.TrimSpace(strings.ReplaceAll(valUpper, "L", ""))
		}
		// Only take the first numeric part
		re := regexp.MustCompile(`[0-9.]+`)
		val = re.FindString(val)

		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f * multiplier
		}
		return 0
	}
	return 0
}

func (pe *ParameterExtractor) normalizeParameters(params *ExtractedParameters) error {
	params.Industry = strings.TrimSpace(params.Industry)
	params.Category = strings.TrimSpace(params.Category)
	params.Subcategory = strings.TrimSpace(params.Subcategory)

	if params.Location != nil {
		if params.Location.Country == "" {
			params.Location.Country = "India"
		}
		// Location.City and Location.State are already title-cased by parseLocationString
		// DO NOT re-title-case here as it breaks multi-word names (e.g., Delhi NCR)
		params.Location.City = strings.TrimSpace(params.Location.City)
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
