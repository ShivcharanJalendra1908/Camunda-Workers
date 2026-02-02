package models

type QueryType string

const (
	// Core Franchise Queries
	QueryTypeFranchiseFullDetails  QueryType = "franchise_full_details"
	QueryTypeFranchiseOutlets      QueryType = "franchise_outlets"
	QueryTypeFranchiseVerification QueryType = "franchise_verification"
	QueryTypeFranchiseDetails      QueryType = "franchise_details"

	// User Queries
	QueryTypeUserProfile QueryType = "user_profile"

	// ===== NEW QUERY TYPES FOR WORKFLOWS =====

	// Home Page
	QueryTypeIndustriesTop9  QueryType = "INDUSTRIES_TOP_9"
	QueryTypeCategoriesTop30 QueryType = "CATEGORIES_TOP_30"

	// Home Page ES Queries
	ESQueryTypeHeroBrands      QueryType = "ES_HERO_BRANDS"
	ESQueryTypePopularListings QueryType = "ES_POPULAR_LISTINGS"

	// Listing Page
	QueryTypeCategoriesFeatured8 QueryType = "CATEGORIES_FEATURED_8"
	QueryTypeIndustryBySlug      QueryType = "INDUSTRY_BY_SLUG"

	// Listing Page ES
	ESQueryTypeFranchiseListing      QueryType = "FRANCHISE_LISTING"
	ESQueryTypeRecommendedByIndustry QueryType = "RECOMMENDED_BY_INDUSTRY"

	// Detail Page
	QueryTypeFranchiseOverview   QueryType = "FRANCHISE_OVERVIEW"
	QueryTypeFranchiseBusiness   QueryType = "FRANCHISE_BUSINESS"
	QueryTypeFranchiseInvestment QueryType = "FRANCHISE_INVESTMENT"
	QueryTypeFranchiseOperations QueryType = "FRANCHISE_OPERATIONS"
	QueryTypeFranchiseSocial     QueryType = "FRANCHISE_SOCIAL"

	// Detail Page ES Queries
	ESQueryTypeFranchiseBySlug QueryType = "FRANCHISE_BY_SLUG"
	ESQueryTypeIndustryBySlug  QueryType = "INDUSTRY_BY_SLUG"
	ESQueryTypeRecommended     QueryType = "RECOMMENDED"
	ESQueryTypeMarketInsights  QueryType = "MARKET_INSIGHTS"

	// Search Queries
	ESQueryTypeSearchWithFilters QueryType = "SEARCH_WITH_FILTERS"

	QueryTypeIndustryBySlugWithQuestions  QueryType = "INDUSTRY_BY_SLUG_WITH_QUESTIONS"
	QueryTypeCategoryQuestionsByIndustry  QueryType = "CATEGORY_QUESTIONS_BY_INDUSTRY"
	QueryTypeFeaturedCategoriesByIndustry QueryType = "FEATURED_CATEGORIES_BY_INDUSTRY"

	ESQueryTypeSearchWithAggregations QueryType = "ES_SEARCH_WITH_AGGREGATIONS"
	ESQueryTypeGetStats               QueryType = "ES_GET_STATS"
	ESQueryTypeGetSuggestions         QueryType = "ES_GET_SUGGESTIONS"
	ESQueryTypeGetByID                QueryType = "ES_GET_BY_ID"
	ESQueryTypeCountByFilter          QueryType = "ES_COUNT_BY_FILTER"
)
