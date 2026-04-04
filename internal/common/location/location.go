package location

import "strings"

// CityAliases — known alternate spellings/names
var CityAliases = map[string][]string{
	"bangalore":     {"Bengaluru", "bengaluru", "Bangalore", "bangalore", "BANGALORE"},
	"bengaluru":     {"Bangalore", "bangalore", "Bengaluru", "bengaluru", "BENGALURU"},
	"mumbai":        {"Mumbai", "mumbai", "Bombay", "bombay", "MUMBAI"},
	"delhi":         {"Delhi", "delhi", "New Delhi", "new delhi", "DELHI"},
	"kolkata":       {"Kolkata", "kolkata", "Calcutta", "calcutta", "KOLKATA"},
	"chennai":       {"Chennai", "chennai", "Madras", "madras", "CHENNAI"},
	"hyderabad":     {"Hyderabad", "hyderabad", "HYDERABAD"},
	"pune":          {"Pune", "pune", "PUNE"},
	"ahmedabad":     {"Ahmedabad", "ahmedabad", "AHMEDABAD"},
	"jaipur":        {"Jaipur", "jaipur", "JAIPUR"},
	"gurgaon":       {"Gurgaon", "gurgaon", "Gurugram", "gurugram", "GURGAON"},
	"gurugram":      {"Gurugram", "gurugram", "Gurgaon", "gurgaon", "GURUGRAM"},
	"noida":         {"Noida", "noida", "NOIDA"},
	"vizag":         {"Visakhapatnam", "visakhapatnam", "Vizag", "vizag"},
	"visakhapatnam": {"Visakhapatnam", "visakhapatnam", "Vizag", "vizag"},
	"trivandrum":    {"Thiruvananthapuram", "thiruvananthapuram", "Trivandrum", "trivandrum"},
	"cochin":        {"Kochi", "kochi", "Cochin", "cochin"},
	"kochi":         {"Kochi", "kochi", "Cochin", "cochin"},
	"mysuru":        {"Mysore", "mysore", "Mysuru", "mysuru"},
	"mysore":        {"Mysore", "mysore", "Mysuru", "mysuru"},
}

// CityZoneMap — city → regional zone
var CityZoneMap = map[string]string{
	// North
	"delhi": "North Indian Cities", "noida": "North Indian Cities",
	"gurgaon": "North Indian Cities", "gurugram": "North Indian Cities",
	"lucknow": "North Indian Cities", "agra": "North Indian Cities",
	"jaipur": "North Indian Cities", "chandigarh": "North Indian Cities",
	"ludhiana": "North Indian Cities", "jalandhar": "North Indian Cities",
	"amritsar": "North Indian Cities", "dehradun": "North Indian Cities",
	"shimla": "North Indian Cities",

	// South
	"bangalore": "South Indian Cities", "bengaluru": "South Indian Cities",
	"hyderabad": "South Indian Cities", "chennai": "South Indian Cities",
	"kochi": "South Indian Cities", "coimbatore": "South Indian Cities",
	"mysore": "South Indian Cities", "mangalore": "South Indian Cities",
	"vijayawada": "South Indian Cities", "tirupati": "South Indian Cities",

	// West
	"mumbai": "West Indian Cities", "pune": "West Indian Cities",
	"ahmedabad": "West Indian Cities", "surat": "West Indian Cities",
	"vadodara": "West Indian Cities", "nagpur": "West Indian Cities",
	"nashik": "West Indian Cities", "rajkot": "West Indian Cities",
	"goa": "West Indian Cities",

	// East
	"kolkata": "East Indian Cities", "bhubaneswar": "East Indian Cities",
	"patna": "East Indian Cities", "ranchi": "East Indian Cities",
	"guwahati": "East Indian Cities", "bhopal": "East Indian Cities",
	"indore": "East Indian Cities", "raipur": "East Indian Cities",
}

// ZoneKeywords — "north india" → "north indian cities"
var ZoneKeywords = map[string]string{
	"north india": "north indian cities", "north indian": "north indian cities",
	"south india": "south indian cities", "south indian": "south indian cities",
	"west india": "west indian cities", "west indian": "west indian cities",
	"east india": "east indian cities", "east indian": "east indian cities",
	"central india": "west indian cities", "central indian": "west indian cities",
	"northeast india": "east indian cities", "northeast indian": "east indian cities",
	"western india": "west indian cities", "western indian": "west indian cities",
	"northern india": "north indian cities", "northern indian": "north indian cities",
	"southern india": "south indian cities", "southern indian": "south indian cities",
	"eastern india": "east indian cities", "eastern indian": "east indian cities",
}

// CityStateMap — optional, agar dono workers mein hai toh yahan bhi shift kar do
var CityStateMap = map[string]string{
	// apna existing cityStateMap yahan move karo
}

var zoneNormalized = map[string]string{
	"north indian cities": "North Indian Cities",
	"south indian cities": "South Indian Cities",
	"west indian cities":  "West Indian Cities",
	"east indian cities":  "East Indian Cities",
}

// BuildLocationTerms — city/zone string → ES terms slice
func BuildLocationTerms(city string) []string {
	cityLower := strings.ToLower(strings.TrimSpace(city))
	if len(cityLower) == 0 {
		return []string{}
	}

	// Direct zone match
	if zoneTitle, ok := zoneNormalized[cityLower]; ok {
		return []string{zoneTitle, "Pan India", "Pan-India", "All major Indian cities"}
	}

	cityTitle := strings.ToUpper(cityLower[:1]) + cityLower[1:]
	terms := []string{
		cityTitle,
		"Pan India",
		"Pan-India",
		"All major Indian cities",
	}

	if cityLower == "delhi" {
		terms = append(terms, "Delhi NCR")
	}
	if state, ok := CityStateMap[cityLower]; ok {
		terms = append(terms, state)
	}
	if zone, ok := CityZoneMap[cityLower]; ok {
		terms = append(terms, zone)
	}
	if aliases, ok := CityAliases[cityLower]; ok {
		for _, alias := range aliases {
			a := strings.TrimSpace(alias)
			if len(a) > 0 && a[0] >= 'A' && a[0] <= 'Z' {
				terms = append(terms, a)
			}
		}
	}

	return terms
}

// DetectCityFromQuery — "burger in Mumbai" → "Mumbai"
func DetectCityFromQuery(query string) string {
	queryLower := strings.ToLower(strings.TrimSpace(query))

	for keyword, zone := range ZoneKeywords {
		if strings.Contains(queryLower, keyword) {
			return zone
		}
	}

	prepositions := []string{" in ", " at ", " near ", " from ", " around "}
	titleCase := func(s string) string {
		if s == "" {
			return ""
		}
		return strings.ToUpper(s[:1]) + s[1:]
	}

	for cityKey := range CityAliases {
		if queryLower == cityKey {
			return titleCase(cityKey)
		}
		for _, prep := range prepositions {
			if strings.Contains(queryLower, prep+cityKey) {
				return titleCase(cityKey)
			}
		}
		if strings.HasPrefix(queryLower, cityKey+" ") || strings.HasSuffix(queryLower, " "+cityKey) {
			return titleCase(cityKey)
		}
	}

	for cityKey, aliases := range CityAliases {
		for _, alias := range aliases {
			aliasLower := strings.ToLower(alias)
			if queryLower == aliasLower {
				return titleCase(cityKey)
			}
			for _, prep := range prepositions {
				if strings.Contains(queryLower, prep+aliasLower) {
					return titleCase(cityKey)
				}
			}
			if strings.HasPrefix(queryLower, aliasLower+" ") || strings.HasSuffix(queryLower, " "+aliasLower) {
				return titleCase(cityKey)
			}
		}
	}

	return ""
}

// StripLocationFromQuery — "burger in Mumbai" → "burger"
func StripLocationFromQuery(query string) string {
	prepositions := []string{" in ", " at ", " near ", " from ", " around "}
	result := " " + strings.ToLower(strings.TrimSpace(query)) + " "
	for _, prep := range prepositions {
		if idx := strings.Index(result, prep); idx != -1 {
			result = result[:idx]
		}
	}
	return strings.TrimSpace(result)
}
