package location

import (
	"regexp"
	"sort"
	"strings"
)

var CityAliases = map[string][]string{
	"agartala": {"Agartala", "agartala"},
	"agra": {"Agra", "agra"},
	"ahmedabad": {"Ahmedabad", "ahmedabad"},
	"ahmednagar": {"Ahmednagar", "ahmednagar"},
	"akot": {"Akot", "akot"},
	"alibag": {"Alibag", "alibag"},
	"all major indian cities": {"All major Indian cities", "all major indian cities"},
	"allahabad": {"Allahabad", "allahabad"},
	"ambattur": {"Ambattur", "ambattur"},
	"amravati": {"Amravati", "amravati"},
	"amreli": {"Amreli", "amreli"},
	"amtala": {"Amtala", "amtala"},
	"anand": {"Anand", "anand"},
	"andaman": {"Andaman", "andaman"},
	"andaman & nicobar islands": {"Andaman & Nicobar Islands", "andaman & nicobar islands"},
	"andaman and nicobar": {"Andaman and Nicobar", "andaman and nicobar"},
	"andhra pradesh": {"Andhra Pradesh", "andhra pradesh"},
	"ankleshwar": {"Ankleshwar", "ankleshwar"},
	"arani": {"Arani", "arani"},
	"arunachal pradesh": {"Arunachal Pradesh", "arunachal pradesh"},
	"assam": {"Assam", "assam"},
	"aurangabad": {"Aurangabad", "aurangabad"},
	"badlapur": {"Badlapur", "badlapur"},
	"baidyabati": {"Baidyabati", "baidyabati"},
	"balotra": {"Balotra", "balotra"},
	"bangalore": {"Bangalore", "Bengaluru", "bangalore", "bengaluru"},
	"barrackpore": {"Barrackpore", "barrackpore"},
	"begusarai": {"Begusarai", "begusarai"},
	"belagavi": {"Belagavi", "belagavi"},
	"belgaum": {"Belgaum", "belgaum"},
	"bengaluru": {"Bangalore", "Bengaluru", "bangalore", "bengaluru"},
	"bhagalpur": {"Bhagalpur", "bhagalpur"},
	"bharuch": {"Bharuch", "bharuch"},
	"bhavnagar": {"Bhavnagar", "bhavnagar"},
	"bhilwara": {"Bhilwara", "bhilwara"},
	"bhopal": {"Bhopal", "bhopal"},
	"bhubaneswar": {"Bhubaneswar", "bhubaneswar"},
	"bihar": {"Bihar", "bihar"},
	"bilaspur": {"Bilaspur", "bilaspur"},
	"chandigarh": {"Chandigarh", "chandigarh"},
	"chandni chowk": {"Chandni Chowk", "chandni chowk"},
	"chennai": {"Chennai", "Madras", "chennai", "madras"},
	"chhattisgarh": {"Chhattisgarh", "chhattisgarh"},
	"coimbatore": {"Coimbatore", "coimbatore"},
	"colombo": {"Colombo", "colombo"},
	"cuddapah": {"Cuddapah", "cuddapah"},
	"cuttack": {"Cuttack", "cuttack"},
	"daman & diu": {"Daman & Diu", "daman & diu"},
	"daman and diu": {"Daman and Diu", "daman and diu"},
	"davangere": {"Davangere", "davangere"},
	"dehradun": {"Dehradun", "dehradun"},
	"delhi": {"Delhi", "delhi", "New Delhi", "new delhi", "Delhi NCR", "delhi ncr"},
	"delhi ncr": {"Delhi NCR", "delhi ncr", "Delhi", "delhi", "New Delhi", "new delhi"},
	"new delhi": {"New Delhi", "new delhi", "Delhi", "delhi", "Delhi NCR", "delhi ncr"},
	"dhaka": {"Dhaka", "dhaka"},
	"dhanbad": {"Dhanbad", "dhanbad"},
	"dharwad": {"Dharwad", "dharwad"},
	"dibrugarh": {"Dibrugarh", "dibrugarh"},
	"dispur": {"Dispur", "dispur"},
	"dubai": {"Dubai", "dubai"},
	"dwarka": {"Dwarka", "dwarka"},
	"east india": {"East India", "east india"},
	"egra": {"Egra", "egra"},
	"ernakulam": {"Ernakulam", "ernakulam"},
	"erode": {"Erode", "erode"},
	"faridabad": {"Faridabad", "faridabad"},
	"franchise business consultants": {"Franchise Business Consultants", "franchise business consultants"},
	"gachibowli": {"Gachibowli", "gachibowli"},
	"gandhinagar": {"Gandhinagar", "gandhinagar"},
	"ghaziabad": {"Ghaziabad", "ghaziabad"},
	"goa": {"Goa", "goa"},
	"gujarat": {"Gujarat", "gujarat"},
	"guntur": {"Guntur", "guntur"},
	"gurgaon": {"Gurgaon", "gurgaon"},
	"gurugram": {"Gurugram", "gurugram"},
	"guwahati": {"Guwahati", "guwahati"},
	"gwalior": {"Gwalior", "gwalior"},
	"haora": {"Haora", "haora"},
	"haryana": {"Haryana", "haryana"},
	"himachal pradesh": {"Himachal Pradesh", "himachal pradesh"},
	"hooghly": {"Hooghly", "hooghly"},
	"hubballi": {"Hubballi", "hubballi"},
	"hubli": {"Hubli", "hubli"},
	"hyderabad": {"Hyderabad", "hyderabad"},
	"igatpuri": {"Igatpuri", "igatpuri"},
	"indore": {"Indore", "indore"},
	"itanagar": {"Itanagar", "itanagar"},
	"jabalpur": {"Jabalpur", "jabalpur"},
	"jaipur": {"Jaipur", "jaipur"},
	"jalandhar": {"Jalandhar", "jalandhar"},
	"jalgaon": {"Jalgaon", "jalgaon"},
	"jalpaiguri": {"Jalpaiguri", "jalpaiguri"},
	"jammu & kashmir": {"Jammu & Kashmir", "jammu & kashmir"},
	"jammu and kashmir": {"Jammu and Kashmir", "jammu and kashmir"},
	"jamnagar": {"Jamnagar", "jamnagar"},
	"jamshedpur": {"Jamshedpur", "jamshedpur"},
	"jharkhand": {"Jharkhand", "jharkhand"},
	"jodhpur": {"Jodhpur", "jodhpur"},
	"kadapa": {"Kadapa", "kadapa"},
	"kakdwip": {"Kakdwip", "kakdwip"},
	"kalyani": {"Kalyani", "kalyani"},
	"kanpur": {"Kanpur", "kanpur"},
	"karnataka": {"Karnataka", "karnataka"},
	"kerala": {"Kerala", "kerala"},
	"kochi": {"Kochi", "kochi"},
	"kolkata": {"Calcutta", "Kolkata", "calcutta", "kolkata"},
	"kota": {"Kota", "kota"},
	"kottayam": {"Kottayam", "kottayam"},
	"lakshadweep": {"Lakshadweep", "lakshadweep"},
	"lonavala": {"Lonavala", "lonavala"},
	"lucknow": {"Lucknow", "lucknow"},
	"ludhiana": {"Ludhiana", "ludhiana"},
	"madgaon": {"Madgaon", "madgaon"},
	"madhapur": {"Madhapur", "madhapur"},
	"madhya pradesh": {"Madhya Pradesh", "madhya pradesh"},
	"madikeri": {"Madikeri", "madikeri"},
	"mahabaleshwar": {"Mahabaleshwar", "mahabaleshwar"},
	"maharashtra": {"Maharashtra", "maharashtra"},
	"mangalore": {"Mangalore", "mangalore"},
	"manipal": {"Manipal", "manipal"},
	"manipur": {"Manipur", "manipur"},
	"meerut": {"Meerut", "meerut"},
	"meghalaya": {"Meghalaya", "meghalaya"},
	"mizoram": {"Mizoram", "mizoram"},
	"mohali": {"Mohali", "mohali"},
	"mumbai": {"Bombay", "Mumbai", "bombay", "mumbai"},
	"mysore": {"Mysore", "mysore"},
	"mysuru": {"Mysuru", "mysuru"},
	"nagaland": {"Nagaland", "nagaland"},
	"nagaon": {"Nagaon", "nagaon"},
	"nagpur": {"Nagpur", "nagpur"},
	"nashik": {"Nashik", "nashik"},
	"navi mumbai": {"Navi Mumbai", "navi mumbai"},
	"nellore": {"Nellore", "nellore"},
	"noida": {"Noida", "noida"},
	"north india": {"North India", "north india"},
	"north indian cities": {"North Indian Cities", "north indian cities"},
	"odisha": {"Odisha", "odisha"},
	"ongole": {"Ongole", "ongole"},
	"pan india": {"Pan India", "pan india"},
	"pan-india": {"Pan-India", "pan-india"},
	"panvel": {"Panvel", "panvel"},
	"parganas": {"Parganas", "parganas"},
	"patna": {"Patna", "patna"},
	"perungudi": {"Perungudi", "perungudi"},
	"pimpri chinchwad": {"Pimpri Chinchwad", "pimpri chinchwad"},
	"pollachi": {"Pollachi", "pollachi"},
	"pondicherry": {"Pondicherry", "pondicherry"},
	"ponneri": {"Ponneri", "ponneri"},
	"port blair": {"Port Blair", "port blair"},
	"puducherry": {"Puducherry", "puducherry"},
	"pune": {"Pune", "pune"},
	"punjab": {"Punjab", "punjab"},
	"puri": {"Puri", "puri"},
	"puzhal": {"Puzhal", "puzhal"},
	"raipur": {"Raipur", "raipur"},
	"rajahmundry": {"Rajahmundry", "rajahmundry"},
	"rajasthan": {"Rajasthan", "rajasthan"},
	"rajkot": {"Rajkot", "rajkot"},
	"ranchi": {"Ranchi", "ranchi"},
	"rangapara": {"Rangapara", "rangapara"},
	"raurkela civil township": {"Raurkela Civil Township", "raurkela civil township"},
	"sangli": {"Sangli", "sangli"},
	"satara": {"Satara", "satara"},
	"secunderabad": {"Secunderabad", "secunderabad"},
	"sikkim": {"Sikkim", "sikkim"},
	"siliguri": {"Siliguri", "siliguri"},
	"solapur": {"Solapur", "solapur"},
	"south india": {"South India", "south india"},
	"srikakulam": {"Srikakulam", "srikakulam"},
	"srirampur": {"Srirampur", "srirampur"},
	"surat": {"Surat", "surat"},
	"tamil nadu": {"Tamil Nadu", "tamil nadu"},
	"telangana": {"Telangana", "telangana"},
	"thane": {"Thane", "thane"},
	"tinsukia": {"Tinsukia", "tinsukia"},
	"tirupati": {"Tirupati", "tirupati"},
	"tirupur": {"Tirupur", "tirupur"},
	"towns and pocket towns": {"Towns and pocket towns", "towns and pocket towns"},
	"towns and smaller cities": {"Towns and smaller cities", "towns and smaller cities"},
	"tripura": {"Tripura", "tripura"},
	"udaipur": {"Udaipur", "udaipur"},
	"udupi": {"Udupi", "udupi"},
	"ulhasnagar": {"Ulhasnagar", "ulhasnagar"},
	"union territorie": {"Union Territorie", "union territorie"},
	"union territories": {"Union Territories", "union territories"},
	"uttar pradesh": {"Uttar Pradesh", "uttar pradesh"},
	"uttarakhand": {"Uttarakhand", "uttarakhand"},
	"vadodara": {"Vadodara", "vadodara"},
	"vaishali": {"Vaishali", "vaishali"},
	"varanasi": {"Varanasi", "varanasi"},
	"vijayawada": {"Vijayawada", "vijayawada"},
	"visakhapatnam": {"Visakhapatnam", "visakhapatnam"},
	"warangal": {"Warangal", "warangal"},
	"west bengal": {"West Bengal", "west bengal"},
	"west india": {"West India", "west india"},
	"yavatmal": {"Yavatmal", "yavatmal"},
}

var CityStateMap = map[string]string{
	"agartala": "Tripura",
	"agra": "",
	"ahmedabad": "",
	"ahmednagar": "",
	"akot": "",
	"alibag": "Maharashtra",
	"all major indian cities": "Various",
	"allahabad": "",
	"ambattur": "",
	"amravati": "Maharashtra",
	"amreli": "",
	"amtala": "",
	"anand": "",
	"andaman": "",
	"andaman & nicobar islands": "Andaman & Nicobar Islands",
	"andaman and nicobar": "Andaman and Nicobar",
	"andhra pradesh": "",
	"ankleshwar": "",
	"arani": "",
	"arunachal pradesh": "Arunachal Pradesh",
	"assam": "Assam",
	"aurangabad": "Maharashtra",
	"badlapur": "",
	"baidyabati": "",
	"balotra": "",
	"bangalore": "Karnataka",
	"barrackpore": "",
	"begusarai": "Bihar",
	"belagavi": "Karnataka",
	"belgaum": "",
	"bengaluru": "",
	"bhagalpur": "Bihar",
	"bharuch": "",
	"bhavnagar": "",
	"bhilwara": "Rajasthan",
	"bhopal": "",
	"bhubaneswar": "",
	"bihar": "Bihar",
	"bilaspur": "Chhattisgarh",
	"chandigarh": "",
	"chandni chowk": "Delhi",
	"chennai": "",
	"chhattisgarh": "Chhattisgarh",
	"coimbatore": "",
	"colombo": "",
	"cuddapah": "",
	"cuttack": "",
	"daman & diu": "Daman & Diu",
	"daman and diu": "Daman and Diu",
	"davangere": "",
	"dehradun": "Uttarakhand",
	"delhi": "Delhi",
	"delhi ncr": "Delhi",
	"new delhi": "Delhi",
	"dhaka": "",
	"dhanbad": "Jharkhand",
	"dharwad": "",
	"dibrugarh": "",
	"dispur": "",
	"dubai": "",
	"dwarka": "Delhi",
	"east india": "",
	"egra": "",
	"ernakulam": "Kerala",
	"erode": "Tamil Nadu",
	"faridabad": "",
	"franchise business consultants": "",
	"gachibowli": "Telangana",
	"gandhinagar": "",
	"ghaziabad": "",
	"goa": "",
	"gujarat": "Gujarat",
	"guntur": "",
	"gurgaon": "",
	"gurugram": "Haryana",
	"guwahati": "",
	"gwalior": "",
	"haora": "",
	"haryana": "Haryana",
	"himachal pradesh": "Himachal Pradesh",
	"hooghly": "",
	"hubballi": "",
	"hubli": "Karnataka",
	"hyderabad": "",
	"igatpuri": "",
	"indore": "",
	"itanagar": "",
	"jabalpur": "",
	"jaipur": "",
	"jalandhar": "",
	"jalgaon": "Maharashtra",
	"jalpaiguri": "",
	"jammu & kashmir": "Jammu & Kashmir",
	"jammu and kashmir": "Jammu and Kashmir",
	"jamnagar": "",
	"jamshedpur": "",
	"jharkhand": "Jharkhand",
	"jodhpur": "Rajasthan",
	"kadapa": "Andhra Pradesh",
	"kakdwip": "",
	"kalyani": "",
	"kanpur": "",
	"karnataka": "",
	"kerala": "",
	"kochi": "",
	"kolkata": "",
	"kota": "Rajasthan",
	"kottayam": "Kerala",
	"lakshadweep": "Lakshadweep",
	"lonavala": "",
	"lucknow": "",
	"ludhiana": "",
	"madgaon": "",
	"madhapur": "Telangana",
	"madhya pradesh": "Madhya Pradesh",
	"madikeri": "Karnataka",
	"mahabaleshwar": "",
	"maharashtra": "Maharashtra",
	"mangalore": "Karnataka",
	"manipal": "Karnataka",
	"manipur": "Manipur",
	"meerut": "Uttar Pradesh",
	"meghalaya": "Meghalaya",
	"mizoram": "Mizoram",
	"mohali": "",
	"mumbai": "",
	"mysore": "Karnataka",
	"mysuru": "",
	"nagaland": "Nagaland",
	"nagaon": "",
	"nagpur": "",
	"nashik": "",
	"navi mumbai": "Maharashtra",
	"nellore": "",
	"noida": "",
	"north india": "",
	"north indian cities": "Various",
	"odisha": "Odisha",
	"ongole": "",
	"pan india": "",
	"pan-india": "Various",
	"panvel": "",
	"parganas": "",
	"patna": "",
	"perungudi": "",
	"pimpri chinchwad": "",
	"pollachi": "Tamil Nadu",
	"pondicherry": "Pondicherry",
	"ponneri": "",
	"port blair": "",
	"puducherry": "Puducherry",
	"pune": "",
	"punjab": "Punjab",
	"puri": "",
	"puzhal": "",
	"raipur": "Chhattisgarh",
	"rajahmundry": "Andhra Pradesh",
	"rajasthan": "Rajasthan",
	"rajkot": "",
	"ranchi": "",
	"rangapara": "",
	"raurkela civil township": "",
	"sangli": "Maharashtra",
	"satara": "Maharashtra",
	"secunderabad": "",
	"sikkim": "Sikkim",
	"siliguri": "",
	"solapur": "",
	"south india": "",
	"srikakulam": "",
	"srirampur": "",
	"surat": "",
	"tamil nadu": "",
	"telangana": "Telangana",
	"thane": "",
	"tinsukia": "",
	"tirupati": "Andhra Pradesh",
	"tirupur": "Tamil Nadu",
	"towns and pocket towns": "Kerala",
	"towns and smaller cities": "Karnataka",
	"tripura": "Tripura",
	"udaipur": "Rajasthan",
	"udupi": "Karnataka",
	"ulhasnagar": "",
	"union territorie": "",
	"union territories": "",
	"uttar pradesh": "Uttar Pradesh",
	"uttarakhand": "Uttarakhand",
	"vadodara": "",
	"vaishali": "",
	"varanasi": "",
	"vijayawada": "",
	"visakhapatnam": "",
	"warangal": "Telangana",
	"west bengal": "West Bengal",
	"west india": "",
	"yavatmal": "Maharashtra",
}

var CityZoneMap = map[string]string{
	"delhi": "North Indian Cities", "noida": "North Indian Cities", "new delhi": "North Indian Cities",
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
}
var ZoneKeywords = map[string]string{
	"north india":      "North India",
	"north indian":     "North India",
	"south india":      "South India",
	"south indian":     "South India",
	"west india":       "West India",
	"west indian":      "West India",
	"east india":       "East India",
	"east indian":      "East India",
	"central india":    "North India",
	"central indian":   "North India",
	"northeast india":  "East India",
	"northeast indian": "East India",
	"western india":    "West India",
	"northern india":   "North India",
	"southern india":   "South India",
	"eastern india":    "East India",
	"pan india":        "Pan India",
	"pan-india":        "Pan India",
	"all india":        "Pan India",
}

var CanonicalCityMap = map[string]string{
	"bangalore": "bengaluru",
	"gurgaon":   "gurugram",
	"delhi":     "delhi ncr",
	"new delhi": "delhi ncr",
}

func DetectAllCitiesFromQuery(query string) []string {
	queryLower := " " + strings.ToLower(strings.TrimSpace(query)) + " "

	type aliasInfo struct {
		alias string
		key   string
	}
	var all []aliasInfo
	for key, aliases := range CityAliases {
		for _, a := range aliases {
			all = append(all, aliasInfo{strings.ToLower(strings.TrimSpace(a)), key})
		}
	}
	for kw, target := range ZoneKeywords {
		all = append(all, aliasInfo{strings.ToLower(kw), target})
	}

	sort.Slice(all, func(i, j int) bool {
		return len(all[i].alias) > len(all[j].alias)
	})

	found := make(map[string]bool)
	var results []string

	markers := []string{"in ", "at ", "for ", "near ", "within ", "across ", "from "}
	for _, info := range all {
		key := info.key
		if canon, ok := CanonicalCityMap[key]; ok {
			key = canon
		}
		if found[key] {
			continue
		}
		for _, m := range markers {
			if strings.Contains(queryLower, m+info.alias) {
				results = append(results, key)
				found[key] = true
				break
			}
		}
		if !found[key] {
			if strings.Contains(queryLower, info.alias+" mein") || strings.Contains(queryLower, info.alias+" se") {
				results = append(results, key)
				found[key] = true
			}
		}
	}

	for _, info := range all {
		key := info.key
		if canon, ok := CanonicalCityMap[key]; ok {
			key = canon
		}
		if found[key] {
			continue
		}
		re := regexp.MustCompile("(?i)\\b" + regexp.QuoteMeta(info.alias) + "\\b")
		if re.MatchString(queryLower) {
			results = append(results, key)
			found[key] = true
		}
	}

	return results
}

func DetectCityFromQuery(query string) string {
	res := DetectAllCitiesFromQuery(query)
	if len(res) > 0 {
		return res[0]
	}
	return ""
}

func BuildLocationTerms(city string) []string {
	cityLower := strings.ToLower(strings.TrimSpace(city))
	if len(cityLower) == 0 {
		return []string{}
	}

	terms := []string{strings.Title(cityLower)}

	if state, ok := CityStateMap[cityLower]; ok {
		terms = append(terms, state)
	}
	if zone, ok := CityZoneMap[cityLower]; ok {
		terms = append(terms, zone)
		// Extract raw zone name (e.g. "North Indian Cities" -> "North India")
		rawZone := strings.Replace(zone, "Indian Cities", "India", 1)
		terms = append(terms, rawZone)
	}
	if aliases, ok := CityAliases[cityLower]; ok {
		terms = append(terms, aliases...)
	}

	// Always append Pan India terms since they are valid for all city searches
	terms = append(terms, "Pan India", "Pan-India", "All major Indian cities")

	// If it's Delhi, add NCR region cities explicitly
	if cityLower == "delhi" || cityLower == "new delhi" || cityLower == "delhi ncr" {
		terms = append(terms, "Gurgaon", "Gurugram", "Noida", "Faridabad", "Ghaziabad")
	}

	return dedup(terms)
}

func dedup(terms []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, t := range terms {
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			result = append(result, t)
		}
	}
	return result
}

func StripLocationFromQuery(query string) string {
	res := query
	locs := DetectAllCitiesFromQuery(query)
	for _, loc := range locs {
		res = strings.ReplaceAll(res, loc, "")
		if aliases, ok := CityAliases[strings.ToLower(loc)]; ok {
			for _, a := range aliases {
				res = strings.ReplaceAll(res, a, "")
			}
		}
	}
	return strings.TrimSpace(res)
}

