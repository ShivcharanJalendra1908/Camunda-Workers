const fs = require('fs');
const path = require('path');

const filePath = 'internal/workers/infrastructure/build-response/handler.go';
const content = fs.readFileSync(filePath, 'utf8');

// Find the start of the function buildAssociationDetailResponse
const startMarker = 'func (h *Handler) buildAssociationDetailResponse(data map[string]interface{}) map[string]interface{} {';
const endMarker = '\n// ===== LISTING PAGE BUILDER ====='; // We know the next function is built after this marker

const startIndex = content.indexOf(startMarker);
if (startIndex === -1) {
    console.error('Could not find start marker');
    process.exit(1);
}

// Find the next function or comment marking the next block
const endIndex = content.indexOf(endMarker);
if (endIndex === -1) {
    console.error('Could not find end marker');
    process.exit(1);
}

const before = content.substring(0, startIndex);
const after = content.substring(endIndex);

const newFunction = `func (h *Handler) buildAssociationDetailResponse(data map[string]interface{}) map[string]interface{} {
	sections := []interface{}{}

	basicInfo := h.extractMap(data, "basicInfo")
	recommended := h.extractArray(data, "recommended")
	categories := h.extractArray(data, "categories")
	marketInsights := h.extractArray(data, "marketInsights")
	categoryQuestions := h.extractArray(data, "categoryQuestions")

	// Parse association_metadata JSON column safely
	metadataVal := basicInfo["association_metadata"]
	var metadata map[string]interface{}
	if m, ok := metadataVal.(map[string]interface{}); ok {
		metadata = m
	} else if s, ok := metadataVal.(string); ok && s != "" && s != "null" {
		_ = json.Unmarshal([]byte(s), &metadata)
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	overview := getMapVal(metadata, "overview")
	membership_details := getMapVal(metadata, "membership_details")
	services_offered := getArrayVal(metadata, "services_offered")
	programs := getMapVal(metadata, "programs")
	publications := getMapVal(metadata, "publications")
	regional_structure := getMapVal(metadata, "regional_structure")
	events := getMapVal(metadata, "events")
	compliance_policies := getMapVal(metadata, "compliance_policies")
	partnerships := getMapVal(metadata, "partnerships")
	awards := getMapVal(metadata, "awards")
	digital_presence := getArrayVal(metadata, "digital_presence")
	transparency := getArrayVal(metadata, "transparency")
	governance := getMapVal(metadata, "governance")
	data_and_insights := getMapVal(metadata, "data_and_insights")
	contact_details := getMapVal(metadata, "contact_details")

	// 1. association_hero_info_card
	name := getStringVal(basicInfo, "name", "")
	if name == "" {
		name = getStringVal(basicInfo, "brand", "KASSIA")
	}
	slug := getStringVal(basicInfo, "slug", "")
	description := getStringVal(basicInfo, "description", "")

	logoURL := ""
	if logoMap, ok := basicInfo["logo"].(map[string]interface{}); ok {
		logoURL = getStringVal(logoMap, "circle", "")
		if logoURL == "" {
			logoURL = getStringVal(logoMap, "square", "")
		}
	}
	if logoURL == "" {
		logoURL = getStringVal(basicInfo, "logo_url", "/AssociationImages/FeaturedAssociations/kassia.svg")
	}

	tags := getArrayVal(overview, "key_functions")
	var transformedTags []interface{}
	if len(tags) > 0 {
		for _, t := range tags {
			transformedTags = append(transformedTags, t)
		}
	} else {
		transformedTags = []interface{}{
			"12,000+ Members",
			"ISO 9001:2015 Certified",
			"Policy & Grievance Support",
			"Entrepreneur Support",
		}
	}

	socialLinksVal := getMapVal(contact_details, "social_links")
	socialLinks := map[string]interface{}{
		"youtube":   getStringVal(socialLinksVal, "youtube", "https://youtube.com"),
		"facebook":  getStringVal(socialLinksVal, "facebook", "https://facebook.com"),
		"instagram": getStringVal(socialLinksVal, "instagram", "https://instagram.com"),
		"twitter":   getStringVal(socialLinksVal, "twitter", "https://x.com"),
		"linkedin":  getStringVal(socialLinksVal, "linkedin", "https://linkedin.com"),
	}

	heroData := map[string]interface{}{
		"name":         name,
		"slug":         slug,
		"logo":         logoURL,
		"description":  description,
		"likes":        getStringVal(basicInfo, "likes_count", "107"),
		"rating":       getStringVal(basicInfo, "rating", "4.5"),
		"review_count": getStringVal(basicInfo, "rating_count", "99"),
		"tags":         transformedTags,
		"socialLinks":  socialLinks,
	}

	sections = append(sections, map[string]interface{}{
		"type":    "association_hero_info_card",
		"enabled": true,
		"data":    heroData,
	})

	// 2. association_info_grid
	sectorVal := getStringVal(overview, "sector", getStringVal(overview, "sector_represented", ""))
	if sectorVal == "" {
		if industryMap, ok := basicInfo["industry"].(map[string]interface{}); ok {
			sectorVal = getStringVal(industryMap, "name", "")
		}
	}
	if sectorVal == "" {
		sectorVal = "Small scale industries / MSMEs"
	}

	websiteVal := getStringVal(contact_details, "website", "")
	if websiteVal == "" {
		websiteVal = getStringVal(basicInfo, "website_url", "")
	}
	if websiteVal == "" {
		websiteVal = "https://kassia.org.in/"
	}

	phoneVal := getStringVal(contact_details, "phone_number", "")
	if phoneVal == "" {
		phoneVal = getStringVal(overview, "contact_phone", "")
	}
	if phoneVal == "" {
		phoneVal = getStringVal(overview, "email", "")
	}
	if phoneVal == "" {
		phoneVal = "(080) 2335 3250 / 2335 8698"
	}

	infoGridData := map[string]interface{}{
		"association_name":      name,
		"association_type":      getStringVal(metadata, "association_type", getStringVal(overview, "association_type", "Industry body / State-level trade association")),
		"sector":                sectorVal,
		"year_of_establishment": getStringVal(basicInfo, "year_of_establishment", getStringVal(overview, "established_year", "1949")),
		"legal_status":          getStringVal(metadata, "legal_status", getStringVal(overview, "legal_status", "Non-government industry association (trade body)")),
		"headquarters":          getStringVal(contact_details, "office_address", getStringVal(overview, "headquarters_address", "2/106, 17th Cross, Magadi Chord Road, Vijayanagar, Bangalore-560040, Karnataka, India")),
		"regional_presence":     getStringVal(regional_structure, "regional_offices", getStringVal(overview, "regional_presence", "Primarily Karnataka with state & national representation")),
		"website":               websiteVal,
		"contact_details":       phoneVal,
	}

	sections = append(sections, map[string]interface{}{
		"type":    "association_info_grid",
		"enabled": true,
		"data":    infoGridData,
	})

	// 3. membership_section
	membershipList := getArrayVal(membership_details, "categories")
	var transformedMembership []interface{}
	if len(membershipList) > 0 {
		for _, mItem := range membershipList {
			if mm, ok := mItem.(map[string]interface{}); ok {
				transformedMembership = append(transformedMembership, map[string]interface{}{
					"detail":      getStringVal(mm, "name", ""),
					"description": getStringVal(mm, "description", ""),
				})
			}
		}
	} else {
		transformedMembership = []interface{}{
			map[string]interface{}{"detail": "Total Number of Members", "description": "12,000+ members (individual MSME units)"},
			map[string]interface{}{"detail": "Affiliated Associations", "description": "127 affiliated industrial associations across Karnataka"},
			map[string]interface{}{"detail": "Eligibility for Membership", "description": "Small-scale / MSME industrial units operating in Karnataka"},
		}
	}

	eligibilityList := getArrayVal(membership_details, "requirements")
	var transformedEligibility []interface{}
	if len(eligibilityList) > 0 {
		for _, eItem := range eligibilityList {
			if em, ok := eItem.(map[string]interface{}); ok {
				transformedEligibility = append(transformedEligibility, map[string]interface{}{
					"detail":      getStringVal(em, "name", ""),
					"description": getStringVal(em, "description", ""),
				})
			}
		}
	} else {
		transformedEligibility = []interface{}{
			map[string]interface{}{"detail": "Business Type", "description": "MSMEs, manufacturers, startups, and industrial enterprises"},
			map[string]interface{}{"detail": "Location Requirement", "description": "Business operations should be based in Karnataka"},
			map[string]interface{}{"detail": "Documents Required", "description": "GST certificate, business registration, PAN card"},
		}
	}

	applicationVal := getMapVal(membership_details, "application_process")
	var transformedApplication []interface{}
	if len(applicationVal) > 0 {
		for k, v := range applicationVal {
			transformedApplication = append(transformedApplication, map[string]interface{}{
				"detail":      k,
				"description": fmt.Sprintf("%v", v),
			})
		}
	} else {
		transformedApplication = []interface{}{
			map[string]interface{}{"detail": "Application Process", "description": "Submit online membership application form"},
			map[string]interface{}{"detail": "Approval Timeline", "description": "Usually approved within 7–15 business days"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "membership_section",
		"enabled": true,
		"data": map[string]interface{}{
			"Membership":             transformedMembership,
			"Eligibility_Criteria":   transformedEligibility,
			"Membership_Application": transformedApplication,
		},
	})

	// 4. services_and_institutional_offerings
	var transformedServices []interface{}
	if len(services_offered) > 0 {
		for _, sItem := range services_offered {
			if sm, ok := sItem.(map[string]interface{}); ok {
				transformedServices = append(transformedServices, map[string]interface{}{
					"title":       getStringVal(sm, "name", ""),
					"description": getStringVal(sm, "description", ""),
				})
			}
		}
	} else {
		transformedServices = []interface{}{
			map[string]interface{}{"title": "Policy Advocacy", "description": "Actively raises MSME sector policy issues with the Government and participates in policy forums."},
			map[string]interface{}{"title": "Government Representation", "description": "Formal representation on central and state government committees."},
			map[string]interface{}{"title": "Market Access Programs", "description": "Provides buyer–seller information, marketing leads, national & international tender notifications."},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "services_and_institutional_offerings",
		"enabled": true,
		"data":    transformedServices,
	})

	// 5. programs_and_initiatives_section
	var transformedPrograms map[string]interface{}
	if len(programs) > 0 {
		transformedPrograms = map[string]interface{}{}
		for cat, pVal := range programs {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				transformedPrograms[cat] = catList
			}
		}
	} else {
		transformedPrograms = map[string]interface{}{
			"Startup_Programs": []interface{}{
				map[string]interface{}{"title": "MSME Entrepreneurship Promotion", "description": "KASSIA promotes entrepreneurship and supports startup MSMEs through guidance, advocacy, and scheme awareness."},
			},
			"Skill_Development_Initiatives": []interface{}{
				map[string]interface{}{"title": "Industrial Skill Development", "description": "Skill enhancement programs for workforce readiness and MSME industrial growth."},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "programs_and_initiatives_section",
		"enabled": true,
		"data":    transformedPrograms,
	})

	// 6. publications_section
	var transformedPublications map[string]interface{}
	if len(publications) > 0 {
		transformedPublications = map[string]interface{}{}
		for cat, pVal := range publications {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				transformedPublications[cat] = catList
			}
		}
	} else {
		transformedPublications = map[string]interface{}{
			"Industry_Reports": []interface{}{
				map[string]interface{}{"title": "MSME Industry Overview Publications", "description": "General industry insights and MSME sector updates issued through KASSIA platforms."},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "publications_section",
		"enabled": true,
		"data":    transformedPublications,
	})

	// 7. regional_structure_section
	chaptersList := getArrayVal(regional_structure, "chapters")
	var transformedChapters []interface{}
	if len(chaptersList) > 0 {
		for _, cItem := range chaptersList {
			if cm, ok := cItem.(map[string]interface{}); ok {
				transformedChapters = append(transformedChapters, map[string]interface{}{
					"name":                 getStringVal(cm, "name", ""),
					"headquarters_address": getStringVal(cm, "headquarters_address", ""),
					"contact_email":        getStringVal(cm, "contact_email", ""),
				})
			}
		}
	} else {
		transformedChapters = []interface{}{
			map[string]interface{}{
				"name":                 "KASSIA Bengaluru Chapter",
				"headquarters_address": "Magadi Chord Road, Vijayanagar, Bangalore-560040",
				"contact_email":        "info@kassia.org.in",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "regional_structure_section",
		"enabled": true,
		"data": map[string]interface{}{
			"governance_model": getStringVal(regional_structure, "governance_model", "Governing Council consisting of elected representatives from districts and affiliated associations."),
			"headquarters":     getStringVal(regional_structure, "headquarters", "Bengaluru, Karnataka, India"),
			"chapters":         transformedChapters,
		},
	})

	// 8. events_engagement_section
	var transformedEvents map[string]interface{}
	if len(events) > 0 {
		transformedEvents = map[string]interface{}{}
		for cat, eVal := range events {
			if eArr, ok := eVal.([]interface{}); ok {
				var catList []interface{}
				for _, eItem := range eArr {
					if em, ok := eItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(em, "title", ""),
							"description": getStringVal(em, "description", ""),
						})
					}
				}
				transformedEvents[cat] = catList
			}
		}
	} else {
		transformedEvents = map[string]interface{}{
			"Conferences_&_Seminars": []interface{}{
				map[string]interface{}{"title": "India MSME Conclave", "description": "National platform bringing together policy makers, industry leaders, and MSME stakeholders to discuss challenges and growth strategies."},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "events_engagement_section",
		"enabled": true,
		"data":    transformedEvents,
	})

	// 9. compliance_policy_section
	var transformedPolicies map[string]interface{}
	if len(compliance_policies) > 0 {
		transformedPolicies = map[string]interface{}{}
		for cat, pVal := range compliance_policies {
			if pArr, ok := pVal.([]interface{}); ok {
				var catList []interface{}
				for _, pItem := range pArr {
					if pm, ok := pItem.(map[string]interface{}); ok {
						catList = append(catList, map[string]interface{}{
							"title":       getStringVal(pm, "title", ""),
							"description": getStringVal(pm, "description", ""),
						})
					}
				}
				transformedPolicies[cat] = catList
			}
		}
	} else {
		transformedPolicies = map[string]interface{}{
			"Standard_Operating_Guidelines": []interface{}{
				map[string]interface{}{"title": "Udyam Registration Advisory", "description": "Guideline helping micro enterprises register under the updated MSME definition."},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "compliance_policy_section",
		"enabled": true,
		"data":    transformedPolicies,
	})

	// 10. partnership_affiliations_section
	var transformedPartners []interface{}
	partnerList := getArrayVal(partnerships, "list")
	if len(partnerList) > 0 {
		for _, pItem := range partnerList {
			if pm, ok := pItem.(map[string]interface{}); ok {
				transformedPartners = append(transformedPartners, map[string]interface{}{
					"partner_name": getStringVal(pm, "name", ""),
					"relation":     getStringVal(pm, "relation", ""),
					"website":      getStringVal(pm, "website", ""),
				})
			}
		}
	} else {
		transformedPartners = []interface{}{
			map[string]interface{}{
				"partner_name": "Government of Karnataka – Dept. of MSME",
				"relation":     "Policy advocacy, MSME representation, industrial policy inputs",
				"website":      "https://kassia.org.in/about-us/",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "partnership_affiliations_section",
		"enabled": true,
		"data":    transformedPartners,
	})

	// 11. awards_recognition_section
	var transformedAwards []interface{}
	awardList := getArrayVal(awards, "list")
	if len(awardList) > 0 {
		for _, aItem := range awardList {
			if am, ok := aItem.(map[string]interface{}); ok {
				transformedAwards = append(transformedAwards, map[string]interface{}{
					"award_title": getStringVal(am, "title", ""),
					"description": getStringVal(am, "description", ""),
				})
			}
		}
	} else {
		transformedAwards = []interface{}{
			map[string]interface{}{
				"award_title": "ISO 9001:2015 Certification",
				"description": "Certified for maintaining quality standards in providing support services to small-scale industries.",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "awards_recognition_section",
		"enabled": true,
		"data":    transformedAwards,
	})

	// 12. digital_presence_section
	var transformedDigital []interface{}
	if len(digital_presence) > 0 {
		for _, dItem := range digital_presence {
			if dm, ok := dItem.(map[string]interface{}); ok {
				transformedDigital = append(transformedDigital, map[string]interface{}{
					"category":      getStringVal(dm, "category", ""),
					"property_name": getStringVal(dm, "property_name", ""),
					"status":        getStringVal(dm, "status", ""),
					"description":   getStringVal(dm, "description", ""),
				})
			}
		}
	} else {
		transformedDigital = []interface{}{
			map[string]interface{}{
				"category":      "Official Website",
				"property_name": "https://kassia.org.in",
				"status":        "Active",
				"description":   "Primary online hub for member services, Udyam support, news, and notifications.",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "digital_presence_section",
		"enabled": true,
		"data":    transformedDigital,
	})

	// 13. transparency_verification_section
	var transformedTransparency []interface{}
	if len(transparency) > 0 {
		for _, tItem := range transparency {
			if tm, ok := tItem.(map[string]interface{}); ok {
				transformedTransparency = append(transformedTransparency, map[string]interface{}{
					"title":       getStringVal(tm, "title", ""),
					"description": getStringVal(tm, "description", ""),
					"status":      getStringVal(tm, "status", ""),
				})
			}
		}
	} else {
		transformedTransparency = []interface{}{
			map[string]interface{}{"title": "Financial Audit Status", "description": "Audited annually by certified chartered accountants.", "status": "Audited"},
			map[string]interface{}{"title": "Governing Council Disclosures", "description": "Names and designations of all council members publicly disclosed.", "status": "Available"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "transparency_verification_section",
		"enabled": true,
		"data":    transformedTransparency,
	})

	// 14. members_structure_tree
	var structure map[string]interface{}
	if len(governance) > 0 {
		structure = governance
	} else {
		structure = map[string]interface{}{
			"name":        "Sri B.R Ganesh Rao",
			"designation": "President",
			"children": []interface{}{
				map[string]interface{}{
					"name":        "Sri Ninganna S. Biradar",
					"designation": "Vice-President",
				},
				map[string]interface{}{
					"name":        "Sri S.M Hussain",
					"designation": "Hon. General Secretary",
				},
				map[string]interface{}{
					"name":        "Sri Durai R.",
					"designation": "Treasurer",
				},
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "members_structure_tree",
		"enabled": true,
		"data":    structure,
	})

	// 15. recommended_business_associations
	var transformedRecs []interface{}
	if len(recommended) > 0 {
		for _, item := range recommended {
			if rMap, ok := item.(map[string]interface{}); ok {
				transformedRecs = append(transformedRecs, map[string]interface{}{
					"id":       getStringVal(rMap, "id", ""),
					"name":     getStringVal(rMap, "brand", getStringVal(rMap, "name", "")),
					"slug":     getStringVal(rMap, "slug", ""),
					"icon_url": getStringVal(rMap, "logo_url", "/AssociationImages/FeaturedAssociations/ficci.svg"),
				})
			}
		}
	} else {
		transformedRecs = []interface{}{
			map[string]interface{}{"id": "1", "name": "NASSCOM", "slug": "nasscom", "icon_url": "/AssociationImages/FeaturedAssociations/nasscom.svg"},
			map[string]interface{}{"id": "2", "name": "Kassia", "slug": "kassia", "icon_url": "/AssociationImages/FeaturedAssociations/kassia.svg"},
			map[string]interface{}{"id": "3", "name": "FICCI", "slug": "ficci", "icon_url": "/AssociationImages/FeaturedAssociations/ficci.svg"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "recommended_business_associations",
		"enabled": true,
		"data":    transformedRecs,
	})

	// 16. market_insights_section
	var transformedInsights map[string]interface{}
	if len(data_and_insights) > 0 {
		trends := getArrayVal(data_and_insights, "sector_trends")
		var trendList []interface{}
		for _, t := range trends {
			trendList = append(trendList, t)
		}
		transformedInsights = map[string]interface{}{
			"market_stats":  getStringVal(data_and_insights, "market_stats", ""),
			"sector_trends": trendList,
			"exim_data":     getStringVal(data_and_insights, "exim_data", ""),
			"cluster_info":  getStringVal(data_and_insights, "cluster_info", ""),
		}
	} else if len(marketInsights) > 0 {
		if insights, ok := marketInsights[0].(map[string]interface{}); ok {
			trends := getArrayVal(insights, "sector_trends")
			var trendList []interface{}
			for _, t := range trends {
				trendList = append(trendList, t)
			}
			if len(trendList) == 0 {
				trendList = []interface{}{
					"Rising adoption of AI and automation in manufacturing.",
					"Increased focus on sustainable and green manufacturing practices.",
					"Growing integration of MSMEs into global supply chains.",
				}
			}
			transformedInsights = map[string]interface{}{
				"market_stats":  getStringVal(insights, "market_stats", "Karnataka MSME sector contributes 20% to state GDP with over 8 lakh registered units."),
				"sector_trends": trendList,
				"exim_data":     getStringVal(insights, "exim_data", "MSME exports from Karnataka account for approximately $10 billion annually."),
				"cluster_info":  getStringVal(insights, "cluster_info", "Major clusters include Peenya (manufacturing), Belagavi (foundry), and Hubli (valves/machine tools)."),
			}
		}
	}
	if len(transformedInsights) == 0 {
		transformedInsights = map[string]interface{}{
			"market_stats": "Karnataka MSME sector contributes 20% to state GDP with over 8 lakh registered units.",
			"sector_trends": []interface{}{
				"Rising adoption of AI and automation in manufacturing.",
				"Increased focus on sustainable and green manufacturing practices.",
				"Growing integration of MSMEs into global supply chains.",
			},
			"exim_data":    "MSME exports from Karnataka account for approximately $10 billion annually.",
			"cluster_info": "Major clusters include Peenya (manufacturing), Belagavi (foundry), and Hubli (valves/machine tools).",
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "market_insights_section",
		"enabled": true,
		"data":    transformedInsights,
	})

	// 17. featured_business_categories
	var transformedDetailCategories []interface{}
	if len(categories) > 0 {
		for _, item := range categories {
			if cMap, ok := item.(map[string]interface{}); ok {
				transformedDetailCategories = append(transformedDetailCategories, map[string]interface{}{
					"id":       getStringVal(cMap, "id", ""),
					"name":     getStringVal(cMap, "name", ""),
					"slug":     getStringVal(cMap, "slug", ""),
					"icon_url": getStringVal(cMap, "icon_url", "/AssociationImages/FeaturedBusinessCategories/technology.svg"),
				})
			}
		}
	} else {
		transformedDetailCategories = []interface{}{
			map[string]interface{}{"id": "1", "name": "Technology", "slug": "technology", "icon_url": "/AssociationImages/FeaturedBusinessCategories/technology.svg"},
			map[string]interface{}{"id": "2", "name": "Finance", "slug": "finance", "icon_url": "/AssociationImages/FeaturedBusinessCategories/finance.svg"},
			map[string]interface{}{"id": "3", "name": "Healthcare", "slug": "healthcare", "icon_url": "/AssociationImages/FeaturedBusinessCategories/healthcare.svg"},
			map[string]interface{}{"id": "4", "name": "Manufacturing", "slug": "manufacturing", "icon_url": "/AssociationImages/FeaturedBusinessCategories/manufacturing.svg"},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "featured_business_categories",
		"enabled": true,
		"data":    transformedDetailCategories,
	})

	// 18. category_questions
	var detailQuestions []interface{}
	faqList := getArrayVal(metadata, "faqs")
	if len(faqList) > 0 {
		for _, fItem := range faqList {
			if fm, ok := fItem.(map[string]interface{}); ok {
				detailQuestions = append(detailQuestions, map[string]interface{}{
					"question": getStringVal(fm, "question", ""),
					"answer":   getStringVal(fm, "answer", ""),
				})
			}
		}
	}
	if len(detailQuestions) == 0 && len(categoryQuestions) > 0 {
		for _, qItem := range categoryQuestions {
			if qm, ok := qItem.(map[string]interface{}); ok {
				detailQuestions = append(detailQuestions, map[string]interface{}{
					"question": getStringVal(qm, "question", ""),
					"answer":   getStringVal(qm, "answer", ""),
				})
			}
		}
	}
	if len(detailQuestions) == 0 {
		detailQuestions = []interface{}{
			map[string]interface{}{
				"question": "What is the membership process?",
				"answer":   "The membership process involves submitting an online application along with business proof like GST/PAN certificate, followed by approval within 7-15 working days.",
			},
			map[string]interface{}{
				"question": "Does this association support export-import guidelines?",
				"answer":   "Yes, the association regularizes and organizes EXIM workshops, consultancies, and representation on international trade fairs for its premium members.",
			},
		}
	}

	sections = append(sections, map[string]interface{}{
		"type":    "category_questions",
		"enabled": true,
		"data": map[string]interface{}{
			"questions": detailQuestions,
		},
	})

	detailData := map[string]interface{}{
		"pageId":   "association_individual",
		"sections": sections,
	}

	if len(basicInfo) > 0 {
		if id, ok := basicInfo["id"].(string); ok && id != "" {
			detailData["association_id"] = id
		}
		if name, ok := basicInfo["name"].(string); ok && name != "" {
			detailData["association_name"] = name
		}
		if slug, ok := basicInfo["slug"].(string); ok && slug != "" {
			detailData["slug"] = slug
		}
		if status, ok := basicInfo["status"].(string); ok && status != "" {
			detailData["status"] = status
		}
		if createdBy, ok := basicInfo["created_by"].(string); ok && createdBy != "" {
			detailData["created_by"] = createdBy
		}
	}

	return map[string]interface{}{
		"success": true,
		"data":    detailData,
		"metadata": map[string]interface{}{
			"generatedAt": time.Now().UTC().Format(time.RFC3339),
			"source":      "workflow",
			"pageType":    "detail",
			"version":     h.config.AppVersion,
		},
	}
}`;

const output = before + newFunction + after;
fs.writeFileSync(filePath, output);
console.log('Successfully replaced buildAssociationDetailResponse!');
