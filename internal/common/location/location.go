package location

import "strings"

var CityAliases = map[string][]string{
	"bangalore":     {"Bengaluru", "bengaluru", "Bangalore", "bangalore", "BANGALORE"},
	"bengaluru":     {"Bangalore", "bangalore", "Bengaluru", "bengaluru", "BENGALURU"},
	"mumbai":        {"Mumbai", "mumbai", "Bombay", "bombay", "MUMBAI"},
	"delhi":         {"Delhi", "delhi", "New Delhi", "new delhi", "DELHI", "Delhi NCR"},
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

var CityZoneMap = map[string]string{
	"delhi": "North Indian Cities", "noida": "North Indian Cities",
	"gurgaon": "North Indian Cities", "gurugram": "North Indian Cities",
	"lucknow": "North Indian Cities", "agra": "North Indian Cities",
	"jaipur": "North Indian Cities", "chandigarh": "North Indian Cities",
	"ludhiana": "North Indian Cities", "jalandhar": "North Indian Cities",
	"amritsar": "North Indian Cities", "dehradun": "North Indian Cities",
	"shimla": "North Indian Cities", "meerut": "North Indian Cities",
	"varanasi": "North Indian Cities", "kanpur": "North Indian Cities",
	"ghaziabad": "North Indian Cities", "faridabad": "North Indian Cities",
	"indore": "North Indian Cities", "bhopal": "North Indian Cities",
	"jabalpur": "North Indian Cities", "gwalior": "North Indian Cities",

	"bangalore": "South Indian Cities", "bengaluru": "South Indian Cities",
	"hyderabad": "South Indian Cities", "chennai": "South Indian Cities",
	"kochi": "South Indian Cities", "coimbatore": "South Indian Cities",
	"mysore": "South Indian Cities", "mangalore": "South Indian Cities",
	"vijayawada": "South Indian Cities", "tirupati": "South Indian Cities",
	"madurai": "South Indian Cities", "vizag": "South Indian Cities",
	"visakhapatnam": "South Indian Cities",

	"mumbai": "West Indian Cities", "pune": "West Indian Cities",
	"ahmedabad": "West Indian Cities", "surat": "West Indian Cities",
	"vadodara": "West Indian Cities", "nagpur": "West Indian Cities",
	"nashik": "West Indian Cities", "rajkot": "West Indian Cities",
	"goa": "West Indian Cities", "aurangabad": "West Indian Cities",

	"kolkata": "East Indian Cities", "bhubaneswar": "East Indian Cities",
	"patna": "East Indian Cities", "ranchi": "East Indian Cities",
	"guwahati": "East Indian Cities", "raipur": "East Indian Cities",
	"dhanbad": "East Indian Cities", "jamshedpur": "East Indian Cities",
	"cuttack": "East Indian Cities", "rourkela": "East Indian Cities",
}

// ZoneKeywords — query mein zone detect karo → ES term
var ZoneKeywords = map[string]string{
	"north india":      "north indian cities",
	"north indian":     "north indian cities",
	"south india":      "south indian cities",
	"south indian":     "south indian cities",
	"west india":       "west indian cities",
	"west indian":      "west indian cities",
	"east india":       "east indian cities",
	"east indian":      "east indian cities",
	"central india":    "north indian cities",
	"central indian":   "north indian cities",
	"northeast india":  "east indian cities",
	"northeast indian": "east indian cities",
	"western india":    "west indian cities",
	"northern india":   "north indian cities",
	"southern india":   "south indian cities",
	"eastern india":    "east indian cities",
	"pan india":        "Pan India",
	"pan-india":        "Pan India",
}

// StateToLocationTerms — state → ES location field terms
var StateToLocationTerms = map[string][]string{
	"Andhra Pradesh": {
		"Visakhapatnam", "Vizag", "Vijayawada", "Tirupati",
		"Guntur", "Nellore", "Kakinada",
		"South Indian Cities", "Pan India", "Pan-India",
	},
	"Assam": {
		"Guwahati", "Silchar", "Dibrugarh", "Jorhat",
		"East Indian Cities", "Pan India", "Pan-India",
	},
	"Bihar": {
		"Patna", "Gaya", "Muzaffarpur", "Bhagalpur",
		"East Indian Cities", "Pan India", "Pan-India",
	},
	"Chhattisgarh": {
		"Raipur", "Bhilai", "Bilaspur", "Durg", "Korba",
		"East Indian Cities", "Pan India", "Pan-India",
	},
	"Delhi NCR": {
		"Delhi", "New Delhi", "Delhi NCR", "Noida", "Gurgaon",
		"Gurugram", "Faridabad", "Ghaziabad",
		"North Indian Cities", "Pan India", "Pan-India", "All major Indian cities",
	},
	"Goa": {
		"Goa", "Panaji", "Margao",
		"West Indian Cities", "Pan India", "Pan-India",
	},
	"Gujarat": {
		"Ahmedabad", "Surat", "Vadodara", "Rajkot", "Gandhinagar", "Bhavnagar",
		"West Indian Cities", "Pan India", "Pan-India",
	},
	"Haryana": {
		"Gurgaon", "Gurugram", "Faridabad", "Panipat", "Karnal",
		"Rohtak", "Hisar", "Ambala", "Sonipat",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Himachal Pradesh": {
		"Shimla", "Manali", "Dharamshala", "Solan",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Jharkhand": {
		"Ranchi", "Jamshedpur", "Dhanbad", "Bokaro", "Hazaribagh",
		"East Indian Cities", "Pan India", "Pan-India",
	},
	"Karnataka": {
		"Bangalore", "Bengaluru", "Mysore", "Mysuru", "Mangalore",
		"Mangaluru", "Hubballi", "Hubli", "Belagavi", "Shivamogga",
		"South Indian Cities", "Pan India", "Pan-India",
	},
	"Kerala": {
		"Kochi", "Cochin", "Thiruvananthapuram", "Trivandrum",
		"Kozhikode", "Thrissur", "Kollam", "Kottayam",
		"South Indian Cities", "Pan India", "Pan-India",
	},
	"Madhya Pradesh": {
		"Bhopal", "Indore", "Gwalior", "Jabalpur", "Ujjain",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Maharashtra": {
		"Mumbai", "Pune", "Nagpur", "Nashik", "Aurangabad",
		"Thane", "Solapur", "Kolhapur",
		"West Indian Cities", "Pan India", "Pan-India",
	},
	"Odisha": {
		"Bhubaneswar", "Cuttack", "Rourkela", "Berhampur", "Sambalpur",
		"East Indian Cities", "Pan India", "Pan-India",
	},
	"Punjab": {
		"Ludhiana", "Amritsar", "Jalandhar", "Patiala", "Bathinda", "Mohali",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Rajasthan": {
		"Jaipur", "Jodhpur", "Udaipur", "Kota", "Ajmer", "Bikaner",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Tamil Nadu": {
		"Chennai", "Coimbatore", "Madurai", "Tiruchirappalli", "Salem",
		"Tirunelveli", "Vellore",
		"South Indian Cities", "Pan India", "Pan-India",
	},
	"Telangana": {
		"Hyderabad", "Warangal", "Nizamabad", "Karimnagar",
		"South Indian Cities", "Pan India", "Pan-India",
	},
	"Uttar Pradesh": {
		"Lucknow", "Kanpur", "Agra", "Varanasi", "Prayagraj", "Allahabad",
		"Meerut", "Ghaziabad", "Bareilly", "Moradabad", "Gorakhpur", "Noida",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"Uttarakhand": {
		"Dehradun", "Haridwar", "Rishikesh", "Nainital",
		"North Indian Cities", "Pan India", "Pan-India",
	},
	"West Bengal": {
		"Kolkata", "Calcutta", "Howrah", "Durgapur", "Asansol", "Siliguri",
		"East Indian Cities", "Pan India", "Pan-India",
	},
}

var CityStateMap = map[string]string{
	"mumbai": "Maharashtra", "pune": "Maharashtra", "nagpur": "Maharashtra",
	"delhi": "Delhi NCR", "noida": "Delhi NCR", "gurgaon": "Delhi NCR",
	"gurugram": "Delhi NCR", "faridabad": "Delhi NCR", "ghaziabad": "Delhi NCR",
	"bangalore": "Karnataka", "bengaluru": "Karnataka",
	"mysore": "Karnataka", "mysuru": "Karnataka",
	"hyderabad": "Telangana",
	"chennai":   "Tamil Nadu", "coimbatore": "Tamil Nadu",
	"kolkata":   "West Bengal",
	"ahmedabad": "Gujarat", "surat": "Gujarat", "vadodara": "Gujarat",
	"jaipur": "Rajasthan", "jodhpur": "Rajasthan", "udaipur": "Rajasthan",
	"lucknow": "Uttar Pradesh", "kanpur": "Uttar Pradesh", "meerut": "Uttar Pradesh",
	"patna":  "Bihar",
	"bhopal": "Madhya Pradesh", "indore": "Madhya Pradesh",
	"ranchi": "Jharkhand", "jamshedpur": "Jharkhand",
	"bhubaneswar": "Odisha",
	"guwahati":    "Assam",
	"chandigarh":  "Punjab", "ludhiana": "Punjab", "amritsar": "Punjab",
	"dehradun": "Uttarakhand",
	"kochi":    "Kerala", "thiruvananthapuram": "Kerala",
	"raipur":        "Chhattisgarh",
	"goa":           "Goa",
	"visakhapatnam": "Andhra Pradesh", "vizag": "Andhra Pradesh",
}

var zoneNormalized = map[string]string{
	"north indian cities": "North Indian Cities",
	"south indian cities": "South Indian Cities",
	"west indian cities":  "West Indian Cities",
	"east indian cities":  "East Indian Cities",
}

// BuildLocationTerms — city/state/zone → ES terms
func BuildLocationTerms(city string) []string {
	cityLower := strings.ToLower(strings.TrimSpace(city))
	if len(cityLower) == 0 {
		return []string{}
	}

	// 1. State check — full state ke cities return karo
	titleCity := strings.ToUpper(city[:1]) + city[1:]
	if stateTerms, ok := StateToLocationTerms[titleCity]; ok {
		return dedup(stateTerms)
	}

	// 2. Zone normalized check
	if zoneTitle, ok := zoneNormalized[cityLower]; ok {
		return []string{zoneTitle, "Pan India", "Pan-India", "All major Indian cities"}
	}

	// 3. Zone keyword check
	if esZone, ok := ZoneKeywords[cityLower]; ok {
		if normalized, ok := zoneNormalized[strings.ToLower(esZone)]; ok {
			return []string{normalized, "Pan India", "Pan-India", "All major Indian cities"}
		}
		return []string{esZone, "Pan India", "Pan-India", "All major Indian cities"}
	}

	// 4. Regular city
	cityTitle := strings.ToUpper(cityLower[:1]) + cityLower[1:]
	terms := []string{cityTitle, "Pan India", "Pan-India", "All major Indian cities"}

	if cityLower == "delhi" {
		terms = append(terms, "Delhi NCR", "New Delhi")
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

	return dedup(terms)
}

func dedup(terms []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, t := range terms {
		if t != "" && !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

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
		}
	}

	return ""
}

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

// package location

// import "strings"

// // CityAliases — known alternate spellings/names
// var CityAliases = map[string][]string{
// 	"bangalore":     {"Bengaluru", "bengaluru", "Bangalore", "bangalore", "BANGALORE"},
// 	"bengaluru":     {"Bangalore", "bangalore", "Bengaluru", "bengaluru", "BENGALURU"},
// 	"mumbai":        {"Mumbai", "mumbai", "Bombay", "bombay", "MUMBAI"},
// 	"delhi":         {"Delhi", "delhi", "New Delhi", "new delhi", "DELHI"},
// 	"kolkata":       {"Kolkata", "kolkata", "Calcutta", "calcutta", "KOLKATA"},
// 	"chennai":       {"Chennai", "chennai", "Madras", "madras", "CHENNAI"},
// 	"hyderabad":     {"Hyderabad", "hyderabad", "HYDERABAD"},
// 	"pune":          {"Pune", "pune", "PUNE"},
// 	"ahmedabad":     {"Ahmedabad", "ahmedabad", "AHMEDABAD"},
// 	"jaipur":        {"Jaipur", "jaipur", "JAIPUR"},
// 	"gurgaon":       {"Gurgaon", "gurgaon", "Gurugram", "gurugram", "GURGAON"},
// 	"gurugram":      {"Gurugram", "gurugram", "Gurgaon", "gurgaon", "GURUGRAM"},
// 	"noida":         {"Noida", "noida", "NOIDA"},
// 	"vizag":         {"Visakhapatnam", "visakhapatnam", "Vizag", "vizag"},
// 	"visakhapatnam": {"Visakhapatnam", "visakhapatnam", "Vizag", "vizag"},
// 	"trivandrum":    {"Thiruvananthapuram", "thiruvananthapuram", "Trivandrum", "trivandrum"},
// 	"cochin":        {"Kochi", "kochi", "Cochin", "cochin"},
// 	"kochi":         {"Kochi", "kochi", "Cochin", "cochin"},
// 	"mysuru":        {"Mysore", "mysore", "Mysuru", "mysuru"},
// 	"mysore":        {"Mysore", "mysore", "Mysuru", "mysuru"},
// }

// // CityZoneMap — city → regional zone
// var CityZoneMap = map[string]string{
// 	// North
// 	"delhi": "North Indian Cities", "noida": "North Indian Cities",
// 	"gurgaon": "North Indian Cities", "gurugram": "North Indian Cities",
// 	"lucknow": "North Indian Cities", "agra": "North Indian Cities",
// 	"jaipur": "North Indian Cities", "chandigarh": "North Indian Cities",
// 	"ludhiana": "North Indian Cities", "jalandhar": "North Indian Cities",
// 	"amritsar": "North Indian Cities", "dehradun": "North Indian Cities",
// 	"shimla": "North Indian Cities",

// 	// South
// 	"bangalore": "South Indian Cities", "bengaluru": "South Indian Cities",
// 	"hyderabad": "South Indian Cities", "chennai": "South Indian Cities",
// 	"kochi": "South Indian Cities", "coimbatore": "South Indian Cities",
// 	"mysore": "South Indian Cities", "mangalore": "South Indian Cities",
// 	"vijayawada": "South Indian Cities", "tirupati": "South Indian Cities",

// 	// West
// 	"mumbai": "West Indian Cities", "pune": "West Indian Cities",
// 	"ahmedabad": "West Indian Cities", "surat": "West Indian Cities",
// 	"vadodara": "West Indian Cities", "nagpur": "West Indian Cities",
// 	"nashik": "West Indian Cities", "rajkot": "West Indian Cities",
// 	"goa": "West Indian Cities",

// 	// East
// 	"kolkata": "East Indian Cities", "bhubaneswar": "East Indian Cities",
// 	"patna": "East Indian Cities", "ranchi": "East Indian Cities",
// 	"guwahati": "East Indian Cities", "bhopal": "East Indian Cities",
// 	"indore": "East Indian Cities", "raipur": "East Indian Cities",
// }

// // ZoneKeywords — "north india" → "north indian cities"
// var ZoneKeywords = map[string]string{
// 	"north india": "north indian cities", "north indian": "north indian cities",
// 	"south india": "south indian cities", "south indian": "south indian cities",
// 	"west india": "west indian cities", "west indian": "west indian cities",
// 	"east india": "east indian cities", "east indian": "east indian cities",
// 	"central india": "west indian cities", "central indian": "west indian cities",
// 	"northeast india": "east indian cities", "northeast indian": "east indian cities",
// 	"western india": "west indian cities", "western indian": "west indian cities",
// 	"northern india": "north indian cities", "northern indian": "north indian cities",
// 	"southern india": "south indian cities", "southern indian": "south indian cities",
// 	"eastern india": "east indian cities", "eastern indian": "east indian cities",
// }

// // CityStateMap — optional, agar dono workers mein hai toh yahan bhi shift kar do
// var CityStateMap = map[string]string{
// 	// apna existing cityStateMap yahan move karo
// }

// var zoneNormalized = map[string]string{
// 	"north indian cities": "North Indian Cities",
// 	"south indian cities": "South Indian Cities",
// 	"west indian cities":  "West Indian Cities",
// 	"east indian cities":  "East Indian Cities",
// }

// // BuildLocationTerms — city/zone string → ES terms slice
// func BuildLocationTerms(city string) []string {
// 	cityLower := strings.ToLower(strings.TrimSpace(city))
// 	if len(cityLower) == 0 {
// 		return []string{}
// 	}

// 	// Direct zone match
// 	if zoneTitle, ok := zoneNormalized[cityLower]; ok {
// 		return []string{zoneTitle, "Pan India", "Pan-India", "All major Indian cities"}
// 	}

// 	cityTitle := strings.ToUpper(cityLower[:1]) + cityLower[1:]
// 	terms := []string{
// 		cityTitle,
// 		"Pan India",
// 		"Pan-India",
// 		"All major Indian cities",
// 	}

// 	if cityLower == "delhi" {
// 		terms = append(terms, "Delhi NCR")
// 	}
// 	if state, ok := CityStateMap[cityLower]; ok {
// 		terms = append(terms, state)
// 	}
// 	if zone, ok := CityZoneMap[cityLower]; ok {
// 		terms = append(terms, zone)
// 	}
// 	if aliases, ok := CityAliases[cityLower]; ok {
// 		for _, alias := range aliases {
// 			a := strings.TrimSpace(alias)
// 			if len(a) > 0 && a[0] >= 'A' && a[0] <= 'Z' {
// 				terms = append(terms, a)
// 			}
// 		}
// 	}

// 	return terms
// }

// // DetectCityFromQuery — "burger in Mumbai" → "Mumbai"
// func DetectCityFromQuery(query string) string {
// 	queryLower := strings.ToLower(strings.TrimSpace(query))

// 	for keyword, zone := range ZoneKeywords {
// 		if strings.Contains(queryLower, keyword) {
// 			return zone
// 		}
// 	}

// 	prepositions := []string{" in ", " at ", " near ", " from ", " around "}
// 	titleCase := func(s string) string {
// 		if s == "" {
// 			return ""
// 		}
// 		return strings.ToUpper(s[:1]) + s[1:]
// 	}

// 	for cityKey := range CityAliases {
// 		if queryLower == cityKey {
// 			return titleCase(cityKey)
// 		}
// 		for _, prep := range prepositions {
// 			if strings.Contains(queryLower, prep+cityKey) {
// 				return titleCase(cityKey)
// 			}
// 		}
// 		if strings.HasPrefix(queryLower, cityKey+" ") || strings.HasSuffix(queryLower, " "+cityKey) {
// 			return titleCase(cityKey)
// 		}
// 	}

// 	for cityKey, aliases := range CityAliases {
// 		for _, alias := range aliases {
// 			aliasLower := strings.ToLower(alias)
// 			if queryLower == aliasLower {
// 				return titleCase(cityKey)
// 			}
// 			for _, prep := range prepositions {
// 				if strings.Contains(queryLower, prep+aliasLower) {
// 					return titleCase(cityKey)
// 				}
// 			}
// 			if strings.HasPrefix(queryLower, aliasLower+" ") || strings.HasSuffix(queryLower, " "+aliasLower) {
// 				return titleCase(cityKey)
// 			}
// 		}
// 	}

// 	return ""
// }

// // StripLocationFromQuery — "burger in Mumbai" → "burger"
// func StripLocationFromQuery(query string) string {
// 	prepositions := []string{" in ", " at ", " near ", " from ", " around "}
// 	result := " " + strings.ToLower(strings.TrimSpace(query)) + " "
// 	for _, prep := range prepositions {
// 		if idx := strings.Index(result, prep); idx != -1 {
// 			result = result[:idx]
// 		}
// 	}
// 	return strings.TrimSpace(result)
// }
