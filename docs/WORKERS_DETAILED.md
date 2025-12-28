# Detailed Worker (Handler) Documentation

This document provides comprehensive documentation for all workers (handlers) in the Camunda Workers system. Each worker is a stateless Go service that processes Camunda BPMN tasks.

## Table of Contents

1. [Infrastructure Workers](#infrastructure-workers)
2. [Data Access Workers](#data-access-workers)
3. [AI/ML Workers](#aiml-workers)
4. [Business Logic Workers](#business-logic-workers)
5. [Authentication Workers](#authentication-workers)
6. [Communication Workers](#communication-workers)
7. [CRM Workers](#crm-workers)
8. [Franchise Workers](#franchise-workers)

---

## Infrastructure Workers

### 1. validate-subscription

**Task Type:** `validate-subscription`

**Purpose:** Validates user subscription tier and access permissions before allowing access to premium features.

**Location:** `internal/workers/infrastructure/validate-subscription/`

**How It Works:**
1. Receives `userId` and `subscriptionTier` from workflow variables
2. Checks Redis cache first for subscription data (5-minute TTL)
3. If cache miss, queries PostgreSQL `user_subscriptions` table
4. Validates:
   - Subscription exists and is active (`is_valid = true`)
   - Subscription hasn't expired (checks `expires_at` if present)
   - Tier is valid (`free`, `basic`, `premium`, or `enterprise`)
5. Caches result in Redis for 5 minutes
6. Returns validation status and tier level

**Input Schema:**
```json
{
  "userId": "string (required)",
  "subscriptionTier": "string (enum: free|basic|premium|enterprise)"
}
```

**Output Schema:**
```json
{
  "isValid": "boolean",
  "tierLevel": "string",
  "permissions": ["string"]  // Optional
}
```

**Error Codes:**
- `SUBSCRIPTION_INVALID` - Subscription doesn't exist or is inactive
- `SUBSCRIPTION_EXPIRED` - Subscription has expired
- `SUBSCRIPTION_CHECK_FAILED` - Database query failed (retryable)

**Dependencies:**
- PostgreSQL database connection
- Redis cache connection
- `user_subscriptions` table

**Configuration:**
- Timeout: 10s
- Retries: 3 (for connection failures)
- Cache TTL: 5 minutes

**Workflows Used In:**
- WF_AI_CONVERSATION
- WF_FRANCHISE_APPLICATION
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY

---

### 2. build-response

**Task Type:** `build-response`

**Purpose:** Constructs standardized response payloads for BFF (Backend for Frontend) callbacks using configurable templates.

**Location:** `internal/workers/infrastructure/build-response/`

**How It Works:**
1. Receives `templateId`, `requestId`, and `data` from workflow
2. Loads template definition from JSON registry file (with in-memory caching)
3. Validates input data against template's JSON schema
4. Performs template substitution:
   - Replaces `{{key}}` placeholders with values from input data
   - Supports nested keys using dot notation (e.g., `{{user.profile.name}}`)
   - Handles arrays and nested objects recursively
5. Builds standardized response with metadata (timestamp, version)
6. Returns formatted response payload

**Input Schema:**
```json
{
  "templateId": "string (required)",
  "requestId": "string (required)",
  "data": "object (required)",
  "metadata": "object (optional)"
}
```

**Output Schema:**
```json
{
  "response": {
    "requestId": "string",
    "status": "success|error",
    "data": "object",
    "metadata": {
      "timestamp": "ISO8601 datetime",
      "version": "string"
    }
  }
}
```

**Error Codes:**
- `TEMPLATE_NOT_FOUND` - Template ID doesn't exist in registry
- `TEMPLATE_VALIDATION_FAILED` - Input data doesn't match template schema

**Dependencies:**
- Template registry JSON file (configurable path)
- JSON schema validation library

**Configuration:**
- Timeout: 10s
- Retries: 0 (no retries for template errors)
- Template registry path: `configs/templates.json`
- Cache TTL: 1 hour (in-memory)

**Template Format:**
Templates use `{{key}}` syntax for placeholders. Example:
```json
{
  "id": "franchise-detail-response",
  "template": {
    "franchise": {
      "id": "{{franchiseId}}",
      "name": "{{franchiseName}}",
      "investment": "{{investment.min}}"
    }
  },
  "schema": {
    "type": "object",
    "required": ["franchiseId", "franchiseName"]
  }
}
```

**Workflows Used In:**
- WF_AI_CONVERSATION
- WF_FRANCHISE_APPLICATION
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY

---

### 3. select-template

**Task Type:** `select-template`

**Purpose:** Determines the appropriate response template based on context (subscription tier, route path, confidence score).

**Location:** `internal/workers/infrastructure/select-template/`

**How It Works:**
1. Receives context information (subscription tier, route path, template type hint, confidence score)
2. Applies template selection rules:
   - Premium/Enterprise users get enhanced templates
   - Free users get basic templates
   - Route-specific templates take precedence
   - Confidence score affects template selection for AI responses
3. Returns selected template ID

**Input Schema:**
```json
{
  "subscriptionTier": "string (required, enum: free|basic|premium|enterprise)",
  "bibId": "string (optional)",
  "routePath": "string (optional)",
  "templateType": "string (optional)",
  "confidence": "number (optional, 0-1)"
}
```

**Output Schema:**
```json
{
  "selectedTemplateId": "string"
}
```

**Error Codes:**
- `TEMPLATE_SELECTION_FAILED` - Unable to determine appropriate template

**Configuration:**
- Timeout: 10s
- Retries: 0

**Workflows Used In:**
- WF_AI_CONVERSATION
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY

---

## Data Access Workers

### 4. query-postgresql

**Task Type:** `query-postgresql`

**Purpose:** Generic PostgreSQL query executor with parameterized query types for different data retrieval needs.

**Location:** `internal/workers/data-access/query-postgresql/`

**How It Works:**
1. Receives `queryType` and relevant parameters (franchiseId, userId, filters)
2. Routes to appropriate query builder based on `queryType`:
   - `franchise_full_details` - Complete franchise information
   - `franchise_outlets` - Franchise outlet locations
   - `franchise_verification` - Franchise verification status
   - `franchise_details` - Basic franchise details
   - `user_profile` - User profile information
3. Executes parameterized SQL query with context timeout
4. Returns results with execution metadata

**Input Schema:**
```json
{
  "queryType": "string (required, enum: franchise_full_details|franchise_outlets|franchise_verification|franchise_details|user_profile)",
  "franchiseId": "string (optional)",
  "franchiseIds": ["string"] (optional),
  "userId": "string (optional)",
  "filters": "object (optional)"
}
```

**Output Schema:**
```json
{
  "data": "object|array",
  "rowCount": "integer",
  "queryExecutionTime": "integer (milliseconds)"
}
```

**Error Codes:**
- `DATABASE_CONNECTION_FAILED` - Cannot connect to PostgreSQL
- `QUERY_EXECUTION_FAILED` - Query execution error
- `QUERY_TIMEOUT` - Query exceeded timeout (retryable)
- `INVALID_QUERY_TYPE` - Unknown query type

**Dependencies:**
- PostgreSQL database connection
- Query registry in `queries/` subdirectory

**Configuration:**
- Timeout: 30s
- Retries: 3 (for connection/timeout errors)

**Query Registry:**
Queries are organized in `queries/` directory:
- `registry.go` - Query type routing
- `franchise.go` - Franchise-related queries
- `user.go` - User-related queries

**Workflows Used In:**
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY
- WF_FRANCHISE_APPLICATION

---

### 5. query-elasticsearch

**Task Type:** `query-elasticsearch`

**Purpose:** Generic Elasticsearch query executor for full-text search and filtering.

**Location:** `internal/workers/data-access/query-elasticsearch/`

**How It Works:**
1. Receives `indexName`, `queryType`, `filters`, and optional pagination
2. Routes to appropriate query builder:
   - `franchise_index` - Search franchises by various criteria
   - `related_franchises` - Find related franchises based on category/location
3. Builds Elasticsearch query DSL based on filters
4. Executes search with pagination support
5. Returns results with relevance scores and metadata

**Input Schema:**
```json
{
  "indexName": "string (required)",
  "queryType": "string (required, enum: franchise_index|related_franchises)",
  "filters": "object (required)",
  "franchiseId": "string (optional)",
  "category": "string (optional)",
  "pagination": {
    "from": "integer",
    "size": "integer"
  }
}
```

**Output Schema:**
```json
{
  "data": ["object"],
  "totalHits": "integer",
  "maxScore": "number",
  "took": "integer (milliseconds)"
}
```

**Error Codes:**
- `ELASTICSEARCH_CONNECTION_FAILED` - Cannot connect to Elasticsearch (retryable)
- `SEARCH_QUERY_FAILED` - Query execution error (retryable)
- `SEARCH_TIMEOUT` - Search exceeded timeout (retryable)
- `INDEX_NOT_FOUND` - Specified index doesn't exist

**Dependencies:**
- Elasticsearch client connection
- Query builders in `queries/` subdirectory

**Configuration:**
- Timeout: 30s
- Retries: 3 (for connection/timeout errors)

**Query Builders:**
Located in `queries/` directory:
- `builders.go` - Query DSL builders
- `registry.go` - Query type routing

**Workflows Used In:**
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY

---
### 6. franchise-postgres

**Task Type:** `franchise-postgres`

**Purpose:** Unified worker that handles all PostgreSQL operations for franchise data management across multiple related tables. Supports CRUD operations for franchises, business overviews, investment requirements, operations, social links, stats, category questions, and franchise cities.

**Location:** `internal/workers/data-access/franchise-postgres/`

**How It Works:**
1. Receives job with `operation_type` field to determine which operation to perform
2. Routes to appropriate handler method based on operation type:
   - **Franchise Operations**: CREATE_FRANCHISE, UPDATE_FRANCHISE, GET_FRANCHISE, DELETE_FRANCHISE, GET_FULL_FRANCHISE
   - **Business Overview**: CREATE_BUSINESS_OVERVIEW, UPDATE_BUSINESS_OVERVIEW
   - **Investment**: CREATE_INVESTMENT, UPDATE_INVESTMENT
   - **Operations**: CREATE_OPERATIONS, UPDATE_OPERATIONS
   - **Social Links**: CREATE_SOCIAL_LINKS, UPDATE_SOCIAL_LINKS
   - **Stats**: CREATE_FRANCHISE_STATS, UPDATE_FRANCHISE_STATS
   - **Category Questions**: CREATE_CATEGORY_QUESTION, GET_CATEGORY_QUESTIONS, UPDATE_CATEGORY_QUESTION, DELETE_CATEGORY_QUESTION
   - **Franchise Cities**: CREATE_FRANCHISE_CITY, GET_FRANCHISE_CITIES, DELETE_FRANCHISE_CITY
3. Validates input data (UUIDs, required fields, email formats, URL formats, year ranges)
4. Executes database operations within transactions for data consistency
5. Returns operation-specific output with success status and metadata

**Input Schema (Base):**
```json
{
  "operation_type": "string (required, enum: CREATE_FRANCHISE|UPDATE_FRANCHISE|GET_FRANCHISE|DELETE_FRANCHISE|CREATE_BUSINESS_OVERVIEW|UPDATE_BUSINESS_OVERVIEW|CREATE_INVESTMENT|UPDATE_INVESTMENT|CREATE_OPERATIONS|UPDATE_OPERATIONS|GET_FULL_FRANCHISE|CREATE_SOCIAL_LINKS|UPDATE_SOCIAL_LINKS|CREATE_FRANCHISE_STATS|UPDATE_FRANCHISE_STATS|CREATE_CATEGORY_QUESTION|GET_CATEGORY_QUESTIONS|UPDATE_CATEGORY_QUESTION|DELETE_CATEGORY_QUESTION|CREATE_FRANCHISE_CITY|GET_FRANCHISE_CITIES|DELETE_FRANCHISE_CITY)",
  "updated_by": "string (optional, UUID for admin operations)"
}
```

**Input Schema (CREATE_FRANCHISE):**
```json
{
  "operation_type": "CREATE_FRANCHISE",
  "name": "string (required, max 150 chars)",
  "slug": "string (required)",
  "created_by": "string (required, UUID)",
  "short_description": "string (optional)",
  "description": "string (optional)",
  "founded_year": "integer (optional, 1800-current year)",
  "established_year": "integer (optional, 1800-current year)",
  "trusted_seller": "boolean",
  "verified": "boolean",
  "total_outlets": "integer (>= 0)",
  "units_count": "integer (>= 0)",
  "outlet_range": "string (optional)",
  "industry": "string (optional)",
  "parent_company": "string (optional)",
  "business_type": "string (optional)",
  "leader_name": "string (optional)",
  "leader_role": "string (optional)",
  "contact_email": "string (optional, email format)",
  "logo_url": "string (optional, http/https URL)",
  "instagram_url": "string (optional, URL)",
  "facebook_url": "string (optional, URL)",
  "twitter_url": "string (optional, URL)",
  "linkedin_url": "string (optional, URL)"
}
```

**Input Schema (GET_FRANCHISE):**
```json
{
  "operation_type": "GET_FRANCHISE",
  "franchise_id": "string (optional, UUID)",
  "slug": "string (optional)"
}
```

**Input Schema (GET_FULL_FRANCHISE):**
```json
{
  "operation_type": "GET_FULL_FRANCHISE",
  "franchise_id": "string (optional, UUID)",
  "slug": "string (optional)"
}
```

**Output Schema (Franchise Operations):**
```json
{
  "franchise_id": "string (UUID)",
  "slug": "string",
  "success": "boolean",
  "message": "string",
  "created_at": "string (ISO 8601)",
  "updated_at": "string (ISO 8601)"
}
```

**Output Schema (GET_FRANCHISE):**
```json
{
  "id": "string (UUID)",
  "name": "string",
  "slug": "string",
  "short_description": "string",
  "description": "string",
  "founded_year": "integer",
  "established_year": "integer",
  "trusted_seller": "boolean",
  "verified": "boolean",
  "total_outlets": "integer",
  "units_count": "integer",
  "outlet_range": "string",
  "industry": "string",
  "parent_company": "string",
  "business_type": "string",
  "leader_name": "string",
  "leader_role": "string",
  "contact_email": "string",
  "logo_url": "string",
  "created_by": "string (UUID)",
  "updated_by": "string (UUID)",
  "created_at": "string (ISO 8601)",
  "updated_at": "string (ISO 8601)",
  "stats": {
    "rating": "number",
    "rating_count": "integer",
    "follow_count": "integer",
    "likes_count": "integer",
    "view_count": "integer",
    "save_count": "integer",
    "share_count": "integer",
    "enquiry_count": "integer",
    "news_count": "integer"
  }
}
```

**Output Schema (GET_FULL_FRANCHISE):**
```json
{
  "franchise": { /* Franchise object with stats */ },
  "business_overview": {
    "id": "string (UUID)",
    "products": "array|object (JSON)",
    "services": "array|object (JSON)"
  },
  "investment": {
    "id": "string (UUID)",
    "initial_investment_min": "number",
    "initial_investment_max": "number",
    "franchise_fee": "number",
    "royalty_percentage": "number",
    "marketing_fee_percentage": "number",
    "payback_min_months": "integer",
    "payback_max_months": "integer",
    "roi_min_percentage": "number",
    "roi_max_percentage": "number",
    "monthly_turnover_min": "number",
    "monthly_turnover_max": "number",
    "single_unit_cost_min": "number",
    "single_unit_cost_max": "number",
    "investment_includes": "string"
  },
  "operations": {
    "id": "string (UUID)",
    "space_min_sqft": "integer",
    "space_max_sqft": "integer",
    "required_property_type": "string",
    "staff_required_min": "integer",
    "staff_required_max": "integer",
    "staff_breakdown": "object (JSON)",
    "operating_hours": "string",
    "training_provided": "boolean",
    "training_details": "string",
    "computer_requirements": "string",
    "marketing_support": "string",
    "preferred_locations": "string",
    "qualification_required": "string",
    "supply_chain_support": "string",
    "quality_control": "string"
  },
  "social_links": {
    "id": "string (UUID)",
    "instagram_url": "string",
    "facebook_url": "string",
    "twitter_url": "string",
    "linkedin_url": "string"
  },
  "cities": [
    {
      "id": "string (UUID)",
      "franchise_id": "string (UUID)",
      "city": "string",
      "state": "string",
      "country": "string",
      "created_at": "string (ISO 8601)"
    }
  ]
}
```

**Output Schema (Base Operations):**
```json
{
  "id": "string (UUID, optional)",
  "success": "boolean",
  "message": "string"
}
```

**Error Codes:**
- `PARSE_ERROR` - Failed to parse input JSON (no retries)
- `INVALID_OPERATION` - Unknown operation type (no retries)
- `VALIDATION_ERROR` - Input validation failed (no retries)
- `INVALID_UUID` - Invalid UUID format (no retries)
- `NOT_FOUND` - Franchise/record not found (no retries)
- `DATABASE_ERROR` - Database operation failed (2 retries)
- `OPERATION_ERROR` - Generic operation error (0 retries)

**Dependencies:**
- PostgreSQL database connection
- UUID validation
- Transaction support for data consistency

**Configuration:**
- Timeout: 30s (default, configurable via `RequestTimeout`)
- Retries: 0-2 (depends on error type)
- Max Jobs Active: 10
- Poll Interval: 100ms
- Transaction Timeout: 10s
- Database Max Retries: 3
- Enable Audit Log: true (default)

**Key Features:**
- **Operation-Based Routing**: Single worker handles multiple operation types via `operation_type` field
- **Transaction Support**: All write operations use database transactions for consistency
- **Audit Trail**: Tracks `created_by` and `updated_by` with UUID validation
- **Input Validation**: Validates emails, URLs, years, UUIDs, and required fields
- **Social Links Management**: Handles social media URLs in separate `franchise_social_links` table
- **Stats Integration**: Automatically joins franchise stats when fetching franchise data
- **Full Franchise Retrieval**: `GET_FULL_FRANCHISE` operation fetches all related data in one call
- **Dynamic Updates**: UPDATE operations only modify provided fields (partial updates)
- **Category Questions**: Manages FAQ-style questions per franchise category
- **Franchise Cities**: Tracks cities where franchises operate

**Supported Tables:**
1. `franchises` - Main franchise information
2. `franchise_business_overview` - Products & services
3. `franchise_investment_requirement` - Investment details
4. `franchise_operations` - Space, staff, support requirements
5. `franchise_social_links` - Social media URLs
6. `franchise_stats` - Engagement metrics (views, likes, saves, etc.)
7. `category_questions` - FAQ questions per category
8. `franchise_cities` - Cities where franchise operates

**Workflows Used In:**
- WF_FRANCHISE_DETAIL_PAGE
- WF_FRANCHISE_DISCOVERY
- WF_FRANCHISE_APPLICATION
- Any workflow requiring franchise data management

---

## AI/ML Workers

### 7. parse-user-intent

**Task Type:** `parse-user-intent`

**Purpose:** Analyzes user query intent using NLP/AI to extract intent, entities, and required data sources.

**Location:** `internal/workers/ai-conversation/parse-user-intent/`

**How It Works:**
1. Receives user question and optional conversation context
2. Sends request to GenAI service `/api/ai/parse-intent` endpoint
3. Implements exponential backoff retry logic (up to MaxRetries)
4. Parses AI response containing:
   - Primary intent (e.g., "franchise_search", "general_info")
   - Confidence score (0-1)
   - Extracted entities (franchise names, locations, categories)
   - Required data sources
5. If data sources not provided by AI, determines them based on intent and entities
6. Returns structured intent analysis

**Input Schema:**
```json
{
  "question": "string (required)",
  "context": "object (optional)"
}
```

**Output Schema:**
```json
{
  "intentAnalysis": {
    "primaryIntent": "string",
    "confidence": "number (0-1)"
  },
  "dataSources": ["string"],
  "entities": [
    {
      "type": "string",
      "value": "string"
    }
  ]
}

```

**Error Codes:**
- `INTENT_PARSING_FAILED` - AI service returned error or invalid response (retryable)
- `INTENT_API_TIMEOUT` - Request to AI service timed out (retryable)

**Dependencies:**
- GenAI service HTTP endpoint
- HTTP client with timeout

**Configuration:**
- Timeout: 10s
- Retries: 2 (with exponential backoff)
- GenAI Base URL: Configurable

**Data Source Determination:**
If AI doesn't provide data sources, worker determines them:
- `internal_db` - Always included
- `search_index` - If entities contain franchise_name or category
- `external_web` - If intent is general_info, market_research, or competitor_analysis

**Workflows Used In:**
- WF_AI_CONVERSATION

---

### 8. query-internal-data

**Task Type:** `query-internal-data`

**Purpose:** Retrieves internal franchise data from databases based on extracted entities from user intent.

**Location:** `internal/workers/ai-conversation/query-internal-data/`

**How It Works:**
1. Receives entities (franchise names, locations, categories) and data source requirements
2. Queries internal databases (PostgreSQL, Elasticsearch) based on entities
3. Aggregates data from multiple sources if needed
4. Returns structured internal data for LLM synthesis

**Input Schema:**
```json
{
  "entities": [
    {
      "type": "string",
      "value": "string"
    }
  ],
  "dataSources": ["string"]
}
```

**Output Schema:**
```json
{
  "internalData": "object"
}
```

**Error Codes:**
- `INTERNAL_DATA_QUERY_FAILED` - Database query failed

**Dependencies:**
- PostgreSQL connection
- Elasticsearch connection (if needed)

**Configuration:**
- Timeout: 10s
- Retries: 2

**Workflows Used In:**
- WF_AI_CONVERSATION

---

### 9. enrich-web-search

**Task Type:** `enrich-web-search`

**Purpose:** Enriches AI responses with external web data for market research and general information queries.

**Location:** `internal/workers/ai-conversation/enrich-web-search/`

**How It Works:**
1. Receives user question and extracted entities
2. Performs web search (via external service or API)
3. Filters and ranks results by relevance
4. Extracts relevant snippets and summaries
5. Returns structured web data with sources

**Input Schema:**
```json
{
  "question": "string (required)",
  "entities": [
    {
      "type": "string",
      "value": "string"
    }
  ]
}
```

**Output Schema:**
```json
{
  "webData": {
    "sources": [
      {
        "url": "string",
        "title": "string",
        "snippet": "string",
        "relevance": "number (0-1)"
      }
    ],
    "summary": "string"
  }
}
```

**Error Codes:**
- `WEB_SEARCH_TIMEOUT` - Web search exceeded timeout

**Configuration:**
- Timeout: 3s (short timeout to avoid blocking)
- Retries: 0 (fail fast for web search)

**Workflows Used In:**
- WF_AI_CONVERSATION

---

### 10. llm-synthesis

**Task Type:** `llm-synthesis`

**Purpose:** Synthesizes final AI response using LLM, combining internal data, web data, and user intent.

**Location:** `internal/workers/ai-conversation/llm-synthesis/`

**How It Works:**
1. Receives original question, internal data, web data, and parsed intent
2. Constructs prompt for LLM with all context
3. Sends to LLM service for response generation
4. Extracts response text, confidence score, and cited sources
5. Returns formatted AI response

**Input Schema:**
```json
{
  "question": "string (required)",
  "internalData": "object (required)",
  "webData": "object (required)",
  "intent": "object (required)"
}
```

**Output Schema:**
```json
{
  "llmResponse": "string",
  "confidence": "number (0-1)",
  "sources": ["string"]
}
```

**Error Codes:**
- `LLM_TIMEOUT` - LLM request timed out (retryable)
- `LLM_SYNTHESIS_FAILED` - LLM returned error (retryable)

**Configuration:**
- Timeout: 5s
- Retries: 1

**Workflows Used In:**
- WF_AI_CONVERSATION

---

## Business Logic Workers

### 11. validate-application-data

**Task Type:** `validate-application-data`

**Purpose:** Validates franchise application data for completeness, format, and franchise-specific requirements.

**Location:** `internal/workers/application/validate-application-data/`

**How It Works:**
1. Receives application data and franchise ID
2. Validates three main sections:
   - **Personal Info**: Name (2-100 chars, letters/spaces/hyphens/apostrophes), email (RFC 5322), phone (E.164 format)
   - **Financial Info**: Liquid capital (non-negative), net worth (non-negative), credit score (300-850, optional)
   - **Experience**: Years in industry (non-negative), management experience (boolean)
3. Applies franchise-specific validation rules (minimum capital, credit score requirements)
4. Sanitizes and normalizes data (trim whitespace, remove invalid characters)
5. Returns validated data or detailed validation errors

**Input Schema:**
```json
{
  "applicationData": {
    "personalInfo": {
      "name": "string",
      "email": "string",
      "phone": "string"
    },
    "financialInfo": {
      "liquidCapital": "number",
      "netWorth": "number",
      "creditScore": "number (optional)"
    },
    "experience": {
      "yearsInIndustry": "number",
      "managementExperience": "boolean"
    }
  },
  "franchiseId": "string (required)"
}
```

**Output Schema:**
```json
{
  "isValid": "boolean",
  "validatedData": "object",
  "validationErrors": [
    {
      "field": "string",
      "code": "string",
      "message": "string"
    }
  ]
}
```

**Error Codes:**
- `APPLICATION_VALIDATION_FAILED` - Validation failed (returns detailed errors)

**Validation Rules:**
- Name: 2-100 characters, alphanumeric with spaces/hyphens/apostrophes
- Email: Valid email format
- Phone: E.164 format (digits and optional leading +)
- Financial: Non-negative numbers, franchise-specific minimums
- Credit Score: 300-850 range

**Workflows Used In:**
- WF_FRANCHISE_APPLICATION

---

### 12. check-readiness-score

**Task Type:** `check-readiness-score`

**Purpose:** Calculates franchise seeker readiness score based on application data.

**Location:** `internal/workers/application/check-readiness-score/`

**How It Works:**
1. Receives user ID and application data
2. Calculates scores across four dimensions:
   - **Financial**: Based on liquid capital and net worth relative to franchise requirements
   - **Experience**: Based on years in industry and management experience
   - **Commitment**: Based on application completeness and engagement
   - **Compatibility**: Based on alignment with franchise requirements
3. Computes weighted overall readiness score (0-100)
4. Categorizes qualification level (low, medium, high, excellent)
5. Returns score breakdown

**Input Schema:**
```json
{
  "userId": "string (required)",
  "applicationData": "object (required)"
}
```

**Output Schema:**
```json
{
  "readinessScore": "integer (0-100)",
  "qualificationLevel": "string (enum: low|medium|high|excellent)",
  "scoreBreakdown": {
    "financial": "integer (0-100)",
    "experience": "integer (0-100)",
    "commitment": "integer (0-100)",
    "compatibility": "integer (0-100)"
  }
}
```

**Error Codes:**
- `READINESS_SCORE_FAILED` - Score calculation failed

**Scoring Algorithm:**
- Financial: 30% weight
- Experience: 25% weight
- Location: 20% weight
- Interest: 25% weight

**Workflows Used In:**
- WF_FRANCHISE_APPLICATION

---

### 13. check-priority-routing

**Task Type:** `check-priority-routing`

**Purpose:** Determines application routing priority based on franchise tier (premium vs standard).

**Location:** `internal/workers/application/check-priority-routing/`

**How It Works:**
1. Receives franchise ID
2. Queries franchise database for premium status
3. Determines routing priority:
   - Premium franchisors → High priority
   - Standard franchisors → Medium/Low priority
4. Returns priority level for workflow routing

**Input Schema:**
```json
{
  "franchiseId": "string (required)"
}
```

**Output Schema:**
```json
{
  "isPremiumFranchisor": "boolean",
  "routingPriority": "string (enum: high|medium|low)"
}
```

**Error Codes:**
- `PRIORITY_ROUTING_FAILED` - Unable to determine priority

**Workflows Used In:**
- WF_FRANCHISE_APPLICATION

---

### 14. create-application-record

**Task Type:** `create-application-record`

**Purpose:** Creates franchise application record in database with status tracking.

**Location:** `internal/workers/application/create-application-record/`

**How It Works:**
1. Receives seeker ID, franchise ID, validated application data, readiness score, and priority
2. Generates unique application ID
3. Inserts record into `franchise_applications` table with:
   - Application data (JSON)
   - Readiness score
   - Priority level
   - Initial status ("submitted")
   - Timestamps
4. Returns application ID and status

**Input Schema:**
```json
{
  "seekerId": "string (required)",
  "franchiseId": "string (required)",
  "applicationData": "object (required)",
  "readinessScore": "integer (required)",
  "priority": "string (required)"
}
```

**Output Schema:**
```json
{
  "applicationId": "string",
  "applicationStatus": "string",
  "createdAt": "ISO8601 datetime"
}
```

**Error Codes:**
- `DATABASE_INSERT_FAILED` - Database insert failed (retryable)
- `DUPLICATE_APPLICATION` - Application already exists for this seeker/franchise

**Dependencies:**
- PostgreSQL database
- `franchise_applications` table

**Configuration:**
- Timeout: 10s
- Retries: 3

**Workflows Used In:**
- WF_FRANCHISE_APPLICATION

---

### 15. send-notification

**Task Type:** `send-notification`

**Purpose:** Sends notifications via AWS SES/SNS to franchisors or seekers about application events.

**Location:** `internal/workers/application/send-notification/`

**How It Works:**
1. Receives recipient ID, recipient type (franchisor/seeker), notification type, and optional metadata
2. Determines notification template based on type
3. Sends via AWS SES (email) or SNS (SMS/push)
4. Logs notification delivery status
5. Returns notification ID and status

**Input Schema:**
```json
{
  "recipientId": "string (required)",
  "recipientType": "string (required, enum: franchisor|seeker)",
  "notificationType": "string (required, enum: new_application|application_submitted)",
  "applicationId": "string (optional)",
  "priority": "string (optional)",
  "metadata": "object (optional)"
}
```

**Output Schema:**
```json
{
  "notificationId": "string",
  "status": "string (enum: sent|failed|disabled)",
  "sentAt": "ISO8601 datetime"
}
```

**Error Codes:**
- `NOTIFICATION_SEND_FAILED` - AWS service error (retryable)

**Dependencies:**
- AWS SES client
- AWS SNS client (optional)

**Configuration:**
- Timeout: 30s
- Retries: 3

**Workflows Used In:**
- WF_FRANCHISE_APPLICATION

---

## Authentication Workers

### 16. auth-signin-google

**Task Type:** `auth.signin.google`

**Purpose:** Handles user sign-in via Google OAuth 2.0, authenticates user, and retrieves/creates user in Keycloak.

**Location:** `internal/workers/auth/auth-signin-google/`

**How It Works:**
1. Receives Google OAuth authorization code
2. Exchanges authorization code for access token via Google OAuth API
3. Retrieves user profile from Google (email, name, picture)
4. Checks if user exists in Keycloak by email
5. If user exists: Retrieves user and generates tokens
6. If user doesn't exist: Returns error (sign-in only, not sign-up)
7. Returns user ID, email, name, and authentication tokens

**Input Schema:**
```json
{
  "authCode": "string (required)",
  "email": "string (optional)"
}
```

**Output Schema:**
```json
{
  "success": "boolean",
  "userId": "string",
  "email": "string",
  "firstName": "string",
  "lastName": "string",
  "token": "string"
}
```

**Error Codes:**
- `GOOGLE_OAUTH_ERROR` - Google OAuth exchange failed (retryable)
- `KEYCLOAK_ERROR` - Keycloak operation failed
- `USER_NOT_FOUND` - User doesn't exist (should use sign-up)

**Dependencies:**
- Google OAuth 2.0 API
- Keycloak client
- HTTP client

**Configuration:**
- Timeout: 10s
- Retries: 3

**Security:**
- Validates OAuth state parameter
- Verifies token signature
- Uses secure token storage

---

### 17. auth-signin-linkedin

**Task Type:** `auth.signin.linkedin`

**Purpose:** Handles user sign-in via LinkedIn OAuth 2.0, authenticates user, and retrieves/creates user in Keycloak.

**Location:** `internal/workers/auth/auth-signin-linkedin/`

**How It Works:**
1. Receives LinkedIn OAuth authorization code
2. Exchanges authorization code for access token via LinkedIn OAuth API
3. Retrieves user profile from LinkedIn API (email, name, profile picture)
4. Checks if user exists in Keycloak by email
5. If user exists: Retrieves user and generates tokens
6. If user doesn't exist: Returns error (sign-in only)
7. Returns user ID, email, name, and authentication tokens

**Input Schema:**
```json
{
  "authCode": "string (required, 10-1000 chars)",
  "email": "string (optional, email format)"
}
```

**Output Schema:**
```json
{
  "success": "boolean",
  "userId": "string",
  "email": "string",
  "firstName": "string",
  "lastName": "string",
  "token": "string",
  "timestamp": "ISO8601 datetime"
}
```

**Error Codes:**
- `VALIDATION_FAILED` - Input validation failed
- `MISSING_PARAMETER` - Required parameter missing
- `LINKEDIN_OAUTH_ERROR` - LinkedIn OAuth exchange failed (retryable)
- `LINKEDIN_API_ERROR` - LinkedIn API request failed (retryable)
- `KEYCLOAK_ERROR` - Keycloak operation failed
- `INTERNAL_ERROR` - Internal server error

**Dependencies:**
- LinkedIn OAuth 2.0 API
- Keycloak client
- HTTP client

**Configuration:**
- Timeout: 10s (configurable)
- Retries: 3
- SLA: 500ms target

**Service Layer:**
Uses service layer (`service.go`) for business logic:
- OAuth token exchange
- LinkedIn profile retrieval
- Keycloak user management
- Token generation

---

### 18. auth-signup-google

**Task Type:** `auth.signup.google`

**Purpose:** Handles user sign-up via Google OAuth 2.0, creates new user in Keycloak and optionally in CRM.

**Location:** `internal/workers/auth/auth-signup-google/`

**How It Works:**
1. Receives Google OAuth authorization code
2. Exchanges authorization code for access token
3. Retrieves user profile from Google
4. Checks if user already exists in Keycloak
5. If user exists: Returns error (use sign-in instead)
6. If new user: Creates user in Keycloak with profile data
7. Optionally creates contact in Zoho CRM
8. Generates authentication tokens
9. Returns user ID, email, name, and tokens

**Input Schema:**
```json
{
  "authCode": "string (required)",
  "email": "string (optional)"
}
```

**Output Schema:**
```json
{
  "success": "boolean",
  "userId": "string",
  "email": "string",
  "firstName": "string",
  "lastName": "string",
  "token": "string"
}
```

**Error Codes:**
- `GOOGLE_OAUTH_ERROR` - Google OAuth exchange failed (retryable)
- `KEYCLOAK_ERROR` - Keycloak user creation failed
- `ZOHO_CRM_ERROR` - CRM contact creation failed (non-blocking)

**Dependencies:**
- Google OAuth 2.0 API
- Keycloak client
- Zoho CRM client (optional)

**Configuration:**
- Timeout: 10s
- Retries: 3

---

### 19. auth-signup-linkedin

**Task Type:** `auth.signup.linkedin`

**Purpose:** Handles user sign-up via LinkedIn OAuth 2.0, creates new user in Keycloak and optionally in CRM.

**Location:** `internal/workers/auth/auth-signup-linkedin/`

**How It Works:**
1. Receives LinkedIn OAuth authorization code
2. Exchanges authorization code for access token
3. Retrieves user profile from LinkedIn API
4. Checks if user already exists in Keycloak
5. If user exists: Returns error
6. If new user: Creates user in Keycloak
7. Optionally creates contact in Zoho CRM
8. Generates authentication tokens
9. Returns user information and tokens

**Input Schema:**
```json
{
  "authCode": "string (required)",
  "email": "string (optional)"
}
```

**Output Schema:**
```json
{
  "success": "boolean",
  "userId": "string",
  "email": "string",
  "firstName": "string",
  "lastName": "string",
  "token": "string"
}
```

**Error Codes:**
- `LINKEDIN_OAUTH_ERROR` - LinkedIn OAuth exchange failed (retryable)
- `LINKEDIN_API_ERROR` - LinkedIn API request failed (retryable)
- `KEYCLOAK_ERROR` - Keycloak user creation failed
- `ZOHO_CRM_ERROR` - CRM contact creation failed (non-blocking)

**Dependencies:**
- LinkedIn OAuth 2.0 API
- Keycloak client
- Zoho CRM client (optional)

**Configuration:**
- Timeout: 10s
- Retries: 3

---

### 20. auth-logout

**Task Type:** `auth.logout`

**Purpose:** Handles user logout process, invalidates tokens and clears session.

**Location:** `internal/workers/auth/auth-logout/`

**How It Works:**
1. Receives user ID and refresh token
2. Invalidates refresh token in Keycloak
3. Optionally invalidates access token
4. Clears session data from Redis (if used)
5. Returns logout success status

**Input Schema:**
```json
{
  "userId": "string (required)",
  "refreshToken": "string (required)",
  "accessToken": "string (optional)"
}
```

**Output Schema:**
```json
{
  "success": "boolean",
  "message": "string"
}
```

**Error Codes:**
- `LOGOUT_FAILED` - Logout operation failed

**Dependencies:**
- Keycloak client
- Redis (optional, for session management)

**Configuration:**
- Timeout: 5s
- Retries: 1

---

### 21. captcha-verify

**Task Type:** `security.captcha.verify`

**Purpose:** Verifies CAPTCHA challenge responses to prevent bot attacks.

**Location:** `internal/workers/auth/captcha-verify/`

**How It Works:**
1. Receives CAPTCHA ID and user-provided solution
2. Validates CAPTCHA challenge exists and hasn't expired
3. Compares user solution with stored challenge
4. Checks retry count (prevents brute force)
5. Returns validation result with confidence score

**Input Schema:**
```json
{
  "captchaId": "string (required)",
  "captchaValue": "string (required)",
  "clientIP": "string (optional, IPv4)",
  "userAgent": "string (optional)"
}
```

**Output Schema:**
```json
{
  "valid": "boolean",
  "score": "number (0-1)",
  "retryCount": "integer",
  "errorCode": "string (optional)",
  "errorMessage": "string (optional)"
}
```

**Error Codes:**
- `CAPTCHA_NOT_FOUND` - CAPTCHA ID doesn't exist
- `CAPTCHA_EXPIRED` - CAPTCHA challenge expired
- `INVALID_CAPTCHA` - Solution doesn't match
- `MAX_ATTEMPTS_EXCEEDED` - Too many retry attempts

**Dependencies:**
- Redis (for CAPTCHA storage)
- CAPTCHA generation service

**Configuration:**
- Timeout: 5s
- Retries: 0

---

## Communication Workers

### 22. email-send

**Task Type:** `email.send`

**Purpose:** Sends emails using AWS SES or SMTP with support for HTML, attachments, and custom headers.

**Location:** `internal/workers/communication/email-send/`

**How It Works:**
1. Receives email parameters (to, subject, body, optional: from, cc, bcc, attachments)
2. Validates email addresses and parameters
3. Constructs email message (plain text or HTML)
4. Sends via AWS SES (preferred) or SMTP fallback
5. Logs delivery status and message ID
6. Returns delivery confirmation

**Input Schema:**
```json
{
  "to": "string (required, email format)",
  "subject": "string (required)",
  "body": "string (required)",
  "from": "string (optional, email format)",
  "cc": "string (optional, email format)",
  "bcc": "string (optional, email format)",
  "replyTo": "string (optional, email format)",
  "isHtml": "boolean (optional)",
  "priority": "string (optional)",
  "attachments": [
    {
      "filename": "string",
      "contentType": "string",
      "content": "string (base64)"
    }
  ],
  "metadata": "object (optional)"
}
```

**Output Schema:**
```json
{
  "emailSent": "boolean",
  "emailMessage": "string",
  "messageId": "string (optional)",
  "emailProvider": "string (optional)",
  "sentAt": "ISO8601 datetime (optional)"
}
```

**Error Codes:**
- `SMTP_ERROR` - SMTP connection/send failed (retryable)
- `EMAIL_SEND_FAILED` - Email delivery failed (retryable)

**Dependencies:**
- AWS SES client (primary)
- SMTP server (fallback)
- Service layer for email construction

**Configuration:**
- Timeout: 30s
- Retries: 3
- Default from address: Configurable
- SMTP host/port: Configurable

**Service Layer:**
Uses service layer (`service.go`) for:
- Email message construction
- AWS SES integration
- SMTP fallback
- Attachment handling
- Connection testing

---

## CRM Workers

### 23. crm-user-create

**Task Type:** `crm.user.create`

**Purpose:** Creates a user contact in the CRM system (Zoho CRM) with optional account and lead creation.

**Location:** `internal/workers/crm/crm-user-create/`

**How It Works:**
1. Receives user information (email, name, phone, company, job title, lead source)
2. Validates required fields (email, firstName, lastName)
3. Creates contact in Zoho CRM via API
4. Optionally creates associated account (if company provided)
5. Optionally creates lead record
6. Applies tags and custom fields if provided
7. Returns CRM contact ID and status

**Input Schema:**
```json
{
  "email": "string (required, email format)",
  "firstName": "string (required)",
  "lastName": "string (required)",
  "phone": "string (optional)",
  "company": "string (optional)",
  "jobTitle": "string (optional)",
  "leadSource": "string (optional)",
  "tags": ["string"] (optional),
  "customFields": "object (optional)",
  "metadata": "object (optional)"
}
```

**Output Schema:**
```json
{
  "crmUserCreated": "boolean",
  "crmMessage": "string",
  "crmContactId": "string (optional)",
  "crmAccountId": "string (optional)",
  "crmLeadId": "string (optional)",
  "crmProvider": "string (optional)"
}
```

**Error Codes:**
- `ZOHO_CRM_ERROR` - Zoho CRM API error (retryable)
- `CRM_DUPLICATE_CONTACT` - Contact with email already exists

**Dependencies:**
- Zoho CRM API client
- Zoho OAuth token or API key

**Configuration:**
- Timeout: 10s
- Retries: 3
- Create account: Configurable (default: false)
- Apply tags: Configurable (default: false)

**Service Layer:**
Uses service layer (`service.go`) for:
- Zoho CRM API integration
- Contact creation
- Account/lead creation (optional)
- Error handling
- Connection testing

---

## Franchise Workers

### 24. parse-search-filters

**Task Type:** `parse-search-filters`

**Purpose:** Parses and normalizes search query parameters from user requests into structured filter objects.

**Location:** `internal/workers/franchise/parse-search-filters/`

**How It Works:**
1. Receives raw filter parameters from request
2. Parses and normalizes:
   - Categories (array of strings)
   - Investment range (min/max integers)
   - Locations (array of strings)
   - Keywords (string)
   - Sort criteria (string)
   - Pagination (page/size)
3. Validates filter values
4. Returns structured filter object

**Input Schema:**
```json
{
  "rawFilters": "object (required)"
}
```

**Output Schema:**
```json
{
  "parsedFilters": {
    "categories": ["string"],
    "investmentRange": {
      "min": "integer",
      "max": "integer"
    },
    "locations": ["string"],
    "keywords": "string",
    "sortBy": "string",
    "pagination": {
      "page": "integer",
      "size": "integer"
    }
  }
}
```

**Error Codes:**
- `INVALID_FILTER_FORMAT` - Filter format is invalid

**Configuration:**
- Timeout: 10s
- Retries: 0

**Workflows Used In:**
- WF_FRANCHISE_DISCOVERY

---

### 25. apply-relevance-ranking

**Task Type:** `apply-relevance-ranking`

**Purpose:** Applies custom ranking algorithm to search results based on user profile and franchise characteristics.

**Location:** `internal/workers/franchise/apply-relevance-ranking/`

**How It Works:**
1. Receives search results from Elasticsearch, detailed data from PostgreSQL, and user profile
2. Calculates relevance scores based on:
   - User preferences (location, investment range, interests)
   - Franchise characteristics (rating, popularity, match score)
   - Search query relevance
3. Sorts results by combined relevance score
4. Returns ranked franchise list

**Input Schema:**
```json
{
  "searchResults": ["object"] (required),
  "detailsData": ["object"] (required),
  "userProfile": "object (required)"
}
```

**Output Schema:**
```json
{
  "rankedFranchises": ["object"]
}
```

**Error Codes:**
- `RANKING_FAILED` - Ranking calculation failed

**Configuration:**
- Timeout: 10s
- Retries: 0

**Workflows Used In:**
- WF_FRANCHISE_DISCOVERY

---

### 26. calculate-match-score

**Task Type:** `calculate-match-score`

**Purpose:** Calculates seeker-franchise compatibility score based on financial fit, experience, location, and interests.

**Location:** `internal/workers/franchise/calculate-match-score/`

**How It Works:**
1. Receives user ID (or user profile) and franchise data
2. Retrieves user profile from database (with Redis caching) if not provided
3. Calculates four match factors:
   - **Financial Fit** (30% weight): Compares user capital to franchise investment range
   - **Experience Fit** (25% weight): Based on years in industry
   - **Location Fit** (20% weight): Matches user location preferences with franchise locations
   - **Interest Fit** (25% weight): Matches user interests with franchise category
4. Computes weighted overall match score (0-100)
5. Returns match score and factor breakdown

**Input Schema:**
```json
{
  "userId": "string (required)",
  "franchiseData": {
    "id": "string",
    "investmentMin": "integer",
    "investmentMax": "integer",
    "category": "string",
    "locations": ["string"]
  },
  "userProfile": "object (optional)"
}
```

**Output Schema:**
```json
{
  "matchScore": "integer (0-100)",
  "matchFactors": {
    "financialFit": "integer (0-100)",
    "experienceFit": "integer (0-100)",
    "locationFit": "integer (0-100)",
    "interestFit": "integer (0-100)"
  }
}
```

**Error Codes:**
- `MATCH_SCORE_FAILED` - Score calculation failed

**Dependencies:**
- PostgreSQL database (for user profile)
- Redis cache (for profile caching)

**Configuration:**
- Timeout: 10s
- Retries: 0
- Cache TTL: Configurable

**Scoring Details:**
- Financial Fit:
  - 100: Capital within investment range
  - 80: Capital above max
  - 60: Capital >= 80% of minimum
  - 40: Capital >= 50% of minimum
  - 20: Capital < 50% of minimum
- Experience Fit:
  - 100: 5+ years
  - 80: 3-4 years
  - 60: 1-2 years
  - 30: < 1 year
- Location Fit: 100 if match, 30 if no match, 50 if no preferences
- Interest Fit: 100 if category matches, 40 if no match, 50 if no interests

**Workflows Used In:**
- WF_FRANCHISE_DETAIL_PAGE

---

### 27. search-franchises

**Task Type:** `search-franchises`

**Purpose:** Comprehensive franchise search using Elasticsearch with various filters, sorting, and pagination.

**Location:** `internal/workers/franchise/search-franchises/`

**How It Works:**
1. Receives search parameters (query, category, location, investment range, space, rating, tags, pagination, sorting)
2. Builds Elasticsearch query with filters
3. Executes search with pagination
4. Returns search results with metadata (total count, query time, suggestions)

**Input Schema:**
```json
{
  "query": "string (optional)",
  "category": "string (optional)",
  "location": "string (optional)",
  "min_investment": "number (optional)",
  "max_investment": "number (optional)",
  "min_space": "number (optional)",
  "max_space": "number (optional)",
  "min_rating": "number (optional)",
  "tags": "string (optional)",
  "page": "number (optional, default: 1)",
  "limit": "number (optional, default: 10)",
  "sort_by": "string (optional)",
  "sort_order": "string (optional)",
  "user_id": "string (optional)",
  "session_id": "string (optional)"
}
```

**Output Schema:**
```json
{
  "search_results": "object",
  "success": "boolean",
  "total_count": "number",
  "query_time_ms": "number",
  "suggestions": "string (optional)",
  "error_message": "string (optional)"
}
```

**Dependencies:**
- Elasticsearch client
- Franchise index

**Configuration:**
- Timeout: 60s
- Max jobs active: 5

---

## Common Patterns

### Handler Structure

All handlers follow a consistent structure:

1. **Handler Interface:**
   - `Handle(client worker.JobClient, job entities.Job)` - Main Camunda job handler
   - `Execute(ctx context.Context, input *Input) (*Output, error)` - Business logic execution
   - `completeJob()` - Completes Camunda job with output
   - `failJob()` - Fails Camunda job with error

2. **Error Handling:**
   - Standardized error codes
   - Retry logic based on error type
   - Structured error responses

3. **Logging:**
   - Structured JSON logging
   - Context-aware logging (jobKey, workflowKey)
   - Error logging with full context

4. **Configuration:**
   - Timeout configuration
   - Retry configuration
   - Dependency configuration (DB, cache, APIs)

### Error Handling Strategy

- **Retryable Errors:** Connection failures, timeouts, transient API errors
- **Non-Retryable Errors:** Validation errors, not found errors, business logic errors
- **Error Codes:** Standardized across all workers
- **Error Variables:** Optional error context passed to workflow

### Performance Considerations

- **Caching:** Redis caching for frequently accessed data (subscriptions, profiles)
- **Connection Pooling:** Database and HTTP client connection pooling
- **Timeouts:** Configurable timeouts to prevent hanging requests
- **Retries:** Exponential backoff for retryable errors
- **Metrics:** Prometheus metrics for monitoring (job counts, durations, errors)

### Testing

All handlers include:
- Unit tests with mocked dependencies
- Integration tests with real dependencies (optional)
- Test coverage ≥ 80%

---

## Worker Registration

Workers are registered with Camunda Zeebe in the worker manager (`cmd/worker-manager/main.go`). Each worker:

1. Implements the worker interface
2. Registers with Zeebe using `JobType` and `Handler` function
3. Configures `MaxJobsActive` and `Timeout`
4. Handles graceful shutdown

---

## Monitoring and Observability

All workers expose:
- **Metrics:** Job counts, durations, error rates (Prometheus)
- **Logs:** Structured JSON logs with context
- **Health Checks:** Dependency health (database, cache, APIs)
- **Tracing:** Distributed tracing support (optional)

---

## Configuration

Worker configuration is managed via:
- `configs/config.yaml` - Main configuration file
- Environment variables - Override configuration
- Worker-specific config structs - Type-safe configuration

---

## Best Practices

1. **Stateless Design:** Workers are stateless and can scale horizontally
2. **Idempotency:** Operations are idempotent where possible
3. **Validation:** Input validation before processing
4. **Error Handling:** Comprehensive error handling with retries
5. **Logging:** Detailed logging for debugging and monitoring
6. **Testing:** Comprehensive test coverage
7. **Documentation:** Clear documentation for each worker

---

## Version History

- **v1.0.0** - Initial release with 26 workers
- All workers are production-ready and tested

---

## Support

For issues or questions:
- Check worker-specific README files in `docs/workers/`
- Review error logs with jobKey for debugging
- Consult architecture documentation in `docs/architecture.md`

---

## Main Application Files

### 1. Worker Manager (`cmd/worker-manager/main.go`)

**Purpose:** Main entry point for the worker manager service that registers and manages all Camunda workers.

**Location:** `cmd/worker-manager/main.go`

**How It Works:**

1. **Initialization Phase:**
   - Loads configuration from `configs/config.yaml`
   - Initializes structured logger (zap with console output)
   - Sets up observability (metrics, tracing)
   - Creates context for graceful shutdown

2. **Connection Setup with Retry Logic:**
   - **Zeebe Client:** Connects to Camunda Zeebe broker with exponential backoff (10 retries, 2s initial delay)
   - **PostgreSQL:** Connects to database with retry logic (15 retries, 2s initial delay), tests connection with Ping
   - **Elasticsearch:** Connects to search engine (15 retries, 2s initial delay), tests with Ping
   - **Redis:** Connects to cache (10 retries, 2s initial delay), tests with Ping
   - All connections use `retryWithBackoff` helper function for resilience

3. **External Service Clients:**
   - Initializes Keycloak client for authentication
   - Initializes Zoho CRM client for CRM operations

4. **Worker Registration (27 Workers Total):**
   - **Infrastructure Workers (3):**
     - `validate-subscription` - Validates user subscriptions
     - `build-response` - Builds standardized responses
     - `select-template` - Selects response templates
   
   - **Data Access Workers (3):**
     - `query-postgresql` - PostgreSQL queries
     - `query-elasticsearch` - Elasticsearch queries
     - `franchise-postgres` - Franchise PostgreSQL operations
   
   - **Business Logic Workers (10):**
     - `search-franchises` - Franchise search
     - `parse-search-filters` - Filter parsing
     - `apply-relevance-ranking` - Result ranking
     - `calculate-match-score` - Match scoring
     - `validate-application-data` - Application validation
     - `check-readiness-score` - Readiness scoring
     - `check-priority-routing` - Priority routing
     - `create-application-record` - Application creation
     - `send-notification` - Notification sending
   
   - **AI/ML Workers (4):**
     - `parse-user-intent` - Intent parsing
     - `query-internal-data` - Internal data queries
     - `enrich-web-search` - Web search enrichment
     - `llm-synthesis` - LLM response synthesis
   
   - **Authentication & Utility Workers (8):**
     - `auth-signin-google` - Google sign-in
     - `auth-signin-linkedin` - LinkedIn sign-in
     - `auth-signup-google` - Google sign-up
     - `auth-signup-linkedin` - LinkedIn sign-up
     - `auth-logout` - User logout
     - `captcha-verify` - CAPTCHA verification
     - `email-send` - Email sending
     - `crm-user-create` - CRM user creation

5. **Worker Registration Process:**
   - Checks if worker is enabled in configuration
   - Creates handler with appropriate dependencies (DB, cache, logger)
   - Calls `startWorker()` helper function which:
     - Creates Zeebe job worker with task type
     - Sets max jobs active and timeout from config
     - Registers handler function
     - Opens worker connection

6. **Health & Metrics Server:**
   - Starts HTTP server on port 8080
   - Exposes endpoints:
     - `GET /health` - Health check (returns status and timestamp)
     - `GET /ready` - Readiness check
     - `GET /metrics` - Prometheus metrics endpoint
   - Runs in separate goroutine

7. **Graceful Shutdown:**
   - Listens for SIGINT/SIGTERM signals
   - Closes Zeebe client connection
   - Allows 30 seconds for graceful shutdown
   - Logs shutdown completion

**Key Features:**
- **Retry Logic:** Exponential backoff for all connection attempts
- **Logger Adapters:** Special adapters for AI workers with custom logger interfaces
- **Configuration-Driven:** All workers can be enabled/disabled via config
- **Health Monitoring:** Built-in health and metrics endpoints

**Dependencies:**
- Camunda Zeebe broker
- PostgreSQL database
- Elasticsearch cluster
- Redis cache
- Keycloak (optional)
- Zoho CRM (optional)

**Configuration:**
Workers are configured in `configs/config.yaml`:
```yaml
workers:
  validate-subscription:
    enabled: true
    max_jobs_active: 5
    timeout: 10000  # milliseconds
```

---

### 2. API Gateway (`cmd/api-gateway/main.go`)

**Purpose:** HTTP API gateway that provides REST endpoints for triggering Camunda workflows and direct database operations.

**Location:** `cmd/api-gateway/main.go`

**How It Works:**

1. **Initialization Phase:**
   - Loads configuration
   - Initializes structured logger
   - Sets Gin router mode (Release mode for production)

2. **Service Connections:**
   - **Keycloak:** Initializes authentication client
   - **Camunda:** Connects to Zeebe broker
   - **PostgreSQL:** Connects to database
   - **Elasticsearch:** Connects to search engine
   - **Redis:** Connects to cache
   - All connections are tested and logged

3. **Middleware Setup:**
   - `gin.Recovery()` - Panic recovery
   - `RequestID()` - Request ID generation
   - `Logger()` - Request logging
   - `CORS()` - Cross-origin resource sharing
   - `SecurityHeaders()` - Security headers

4. **Route Groups:**

   **Public Routes (`/api/v1/public`):**
   - **Authentication Workflows:**
     - `POST /auth/google/signup` - Google OAuth signup
     - `POST /auth/google/signin` - Google OAuth signin
     - `POST /auth/linkedin/signup` - LinkedIn OAuth signup
     - `POST /auth/linkedin/signin` - LinkedIn OAuth signin
     - `POST /auth/login` - Email/password login
     - `POST /auth/signup` - Email/password signup
     - `POST /auth/password/reset` - Password reset
     - `POST /auth/logout` - User logout
   
   - **Public Franchise Routes (Direct Handlers):**
     - `GET /franchises/search` - Search franchises
     - `GET /franchises/suggest` - Get suggestions
     - `GET /franchises/stats` - Get statistics
     - `GET /franchises/:id` - Get franchise by ID
     - `GET /franchises/categories` - Get categories
     - `GET /franchises/featured` - Get featured franchises

   **Protected Routes (`/api/v1` - JWT Required):**
   - **AI Workflows:**
     - `POST /ai/query` - Start AI query workflow
     - `POST /ai/discovery` - Start discovery workflow
   
   - **User Management:**
     - `PUT /user/profile` - Update profile (workflow)
     - `DELETE /user/account` - Delete account (workflow)
     - `GET /user/profile` - Get profile (placeholder)
     - `GET /user/preferences` - Get preferences (placeholder)
   
   - **Franchise Operations:**
     - `POST /franchises/search` - Personalized search (workflow)
     - `GET /franchises/details/:id` - Get franchise details (workflow)
     - `POST /franchises/favorite/:id` - Add to favorites (direct)
     - `DELETE /franchises/favorite/:id` - Remove from favorites (direct)
     - `GET /franchises/favorites` - Get favorites (direct)
     - `POST /franchises/save-search` - Save search (direct)
     - `GET /franchises/saved-searches` - Get saved searches (direct)
     - `DELETE /franchises/saved-searches/:id` - Delete saved search (direct)
     - `POST /franchises/create` - Create franchise (workflow)
     - `PUT /franchises/:id` - Update franchise (workflow, admin only)
     - `DELETE /franchises/:id` - Delete franchise (workflow, admin only)
     - `GET /franchises/full/:slug` - Get full franchise (workflow)
   
   - **Application Workflows:**
     - `POST /applications/submit` - Submit application
     - `POST /applications/approve` - Approve activity
   
   - **CRM & Email Workflows:**
     - `POST /crm/sync` - Sync CRM data
     - `POST /email/campaign` - Start email campaign
     - `POST /email/welcome-series` - Start welcome series
   
   - **Social Auth:**
     - `POST /social/auth` - Social auth orchestration
   
   - **Error Handling:**
     - `POST /error/handle` - Error handling workflow

   **Admin Routes (`/api/admin` - JWT + Admin Role):**
   - `GET /workflows` - List active workflows
   - `GET /workflows/:id/status` - Get workflow status
   - `POST /workflows/:id/cancel` - Cancel workflow

5. **Health Check Handler:**
   - Checks PostgreSQL, Redis, and Elasticsearch connections
   - Returns health status (healthy/degraded) based on dependency status
   - Includes dependency status in response

6. **HTTP Server:**
   - Configurable port from config
   - Read/Write/Idle timeouts
   - Graceful shutdown with 30-second timeout
   - Runs in goroutine

7. **Route Summary:**
   - Prints comprehensive route summary on startup
   - Shows all public, protected, and admin routes
   - Displays architecture information

**Key Features:**
- **Dual Architecture:** Workflow-based for complex operations, direct handlers for simple queries
- **JWT Authentication:** Protected routes require valid JWT token
- **Role-Based Access:** Admin routes require admin role
- **Rate Limiting:** Configurable rate limiting for protected routes
- **CORS Support:** Configurable CORS for cross-origin requests

**Handler Initialization:**
- `WorkflowHandler` - Handles all workflow triggers
- `FranchiseHandler` - Handles direct franchise operations

---

### 3. Worker Generator Tool (`cmd/tools/worker-generator/main.go`)

**Purpose:** CLI tool to scaffold new worker implementations from activity registry definitions.

**Location:** `cmd/tools/worker-generator/main.go`

**How It Works:**

1. **Command Line Interface:**
   - `--activity` - Activity ID from registry (required)
   - `--output` - Output directory (default: `./internal/workers/`)
   - `--registry` - Path to activity registry (default: `configs/activity-registry.json`)

2. **Registry Loading:**
   - Loads activity registry JSON file
   - Finds activity by ID
   - Validates activity exists

3. **Template Generation:**
   Generates the following files from templates:
   - **`handler.go`** - Main Camunda job handler
     - Implements `Handle()` method
     - Includes job parsing, validation, execution
     - Error handling and job completion
   
   - **`service.go`** - Business logic service layer
     - Contains `Execute()` method
     - Placeholder for business logic
   
   - **`config.go`** - Worker configuration struct
     - Timeout and worker-specific config
   
   - **`models.go`** - Input/Output structs
     - Generated from JSON schema in registry
     - Includes JSON tags and comments
   
   - **`handler_test.go`** - Unit test template
     - Test cases for service execution
     - Input validation tests
   
   - **`validation.go`** - Input validation logic
     - Placeholder validation method
   
   - **`README.md`** - Worker documentation
     - Generated from registry metadata
     - Includes schemas, error codes, usage examples

4. **Schema Processing:**
   - Parses JSON schema from registry
   - Maps JSON types to Go types
   - Generates struct fields with proper types
   - Adds JSON tags and descriptions

5. **Directory Structure:**
   - Maps category to directory (e.g., "authentication" → "auth")
   - Creates worker directory: `internal/workers/{category}/{activity-id}/`
   - Ensures directory exists before writing files

6. **Template Functions:**
   - `parseSchema()` - Extracts properties from JSON schema
   - `goTypeFromJSONType()` - Maps JSON types to Go types
   - `generateStructFields()` - Generates struct field definitions
   - `upperFirst()` - Capitalizes first letter

**Usage Example:**
```bash
go run cmd/tools/worker-generator/main.go \
  --activity validate-subscription \
  --output ./internal/workers/ \
  --registry configs/activity-registry.json
```

**Output:**
- Creates complete worker scaffold
- Generates all necessary files
- Provides next steps for implementation

---

### 4. Registry Updater Tool (`cmd/tools/registry-updater/main.go`)

**Purpose:** CLI tool to manage activity registry (add, update, validate activities).

**Location:** `cmd/tools/registry-updater/main.go`

**How It Works:**

1. **Commands:**

   **Add Command:**
   - Adds new activity to registry
   - Required flags: `id`, `displayName`, `description`, `category`, `taskType`
   - Optional flags: `version`, `status`
   - Creates activity with default values for optional fields
   - Validates activity doesn't already exist

   **Update Command:**
   - Updates existing activity field
   - Required flags: `id`, `field`, `value`
   - Supported fields: `status`, `version`, `displayName`, `description`, `category`, `taskType`, `timeout`, `retries`
   - Updates `lastUpdated` timestamp

   **Validate Command:**
   - Validates registry file structure
   - Checks for:
     - Duplicate activity IDs
     - Missing required fields (ID, DisplayName, TaskType, Category)
     - Empty registry
   - Reports validation results

2. **Registry Management:**
   - Loads registry from JSON file
   - Creates new registry if file doesn't exist
   - Saves registry with pretty-printed JSON
   - Updates `lastUpdated` timestamp on changes

3. **Validation Rules:**
   - All activities must have unique IDs
   - Required fields: ID, DisplayName, TaskType, Category
   - Reports count of activities found

**Usage Examples:**
```bash
# Add new activity
go run cmd/tools/registry-updater/main.go add \
  -id my-new-worker \
  -displayName "My New Worker" \
  -description "Does something useful" \
  -category infrastructure \
  -taskType my-new-worker

# Update activity status
go run cmd/tools/registry-updater/main.go update \
  -id validate-subscription \
  -field status \
  -value completed

# Validate registry
go run cmd/tools/registry-updater/main.go validate \
  -path configs/activity-registry.json
```

**Error Handling:**
- Validates required flags
- Checks for duplicate IDs
- Validates field names for updates
- Provides helpful error messages

---

## API Handlers

### 1. Workflow Handler (`internal/api/handlers/workflow_handler.go`)

**Purpose:** Handles HTTP requests that trigger Camunda workflows. Acts as the bridge between REST API and workflow engine.

**Location:** `internal/api/handlers/workflow_handler.go`

**How It Works:**

1. **Handler Structure:**
   - Contains Camunda client for workflow operations
   - Contains logger for request logging
   - All methods are Gin HTTP handlers

2. **Workflow Triggering:**
   - Receives HTTP request with JSON payload
   - Extracts JWT claims (user ID, session ID, subscription tier)
   - Validates input data
   - Builds workflow variables from request + JWT claims
   - Generates unique `requestId` using UUID
   - Calls `startWorkflow()` helper to trigger Camunda process
   - Returns workflow instance key and status

3. **Workflow Categories:**

   **AI Conversation Workflows:**
   - `StartAIQuery()` - Triggers `ai_query` workflow
     - Input: question, context, userId
     - Variables: question, userId, sessionId, subscriptionTier, requestId
   
   - `StartDiscovery()` - Triggers `discovery` workflow
     - Input: query, filters, userId
     - Variables: searchQuery, rawFilters, userId, sessionId, subscriptionTier

   **Authentication Workflows:**
   - `StartGoogleSignup()` - Triggers `user-signup-process` with Google provider
   - `StartLinkedInSignup()` - Triggers `user-signup-process` with LinkedIn provider
   - `StartGoogleSignin()` - Triggers `signin-workflow` with Google provider
   - `StartLinkedInSignin()` - Triggers `signin-workflow` with LinkedIn provider
   - `StartUserSignin()` - Triggers `user-login-workflow` for email/password
   - `StartUserSignup()` - Triggers `user-signup-process` for email/password
   - `StartUserLogout()` - Triggers `user-logout-workflow`
   - `StartPasswordReset()` - Triggers `password-reset-workflow`

   **User Management Workflows:**
   - `StartProfileUpdate()` - Triggers `user-profile-update` workflow
   - `StartAccountDeletion()` - Triggers `account-deletion-workflow`

   **Application Workflows:**
   - `StartApplicationProcessing()` - Triggers `application_processing` workflow
     - Input: franchiseId, seekerId, applicationData
   - `StartActivityApproval()` - Triggers `activity-approval-workflow`
     - Input: activityId, activityType, approverId, data

   **Franchise Workflows:**
   - `StartFranchiseSearch()` - Triggers `franchise_detail` workflow
     - Input: query, filters, userId
   - `GetFranchiseDetails()` - Triggers `franchise_detail` workflow
     - Input: franchiseId from URL param
   
   **Franchise CRUD Workflows:**
   - `CreateFranchise()` - Triggers `franchise-postgres-process` with CREATE_FRANCHISE
     - Validates slug format (lowercase, numbers, hyphens)
     - Validates email format
     - Requires authentication
     - Returns workflow instance key
   
   - `UpdateFranchise()` - Triggers `franchise-postgres-process` with UPDATE_FRANCHISE
     - Requires admin role (SYSTEM_ADMIN or ADMIN)
     - Validates email if provided
     - Supports partial updates
   
   - `DeleteFranchise()` - Triggers `franchise-postgres-process` with DELETE_FRANCHISE
     - Requires admin role
     - Soft delete via workflow
   
   - `GetFullFranchise()` - Triggers `franchise-postgres-process` with GET_FULL_FRANCHISE
     - Input: slug from URL param
     - Optional user ID for personalization

   **CRM Workflows:**
   - `StartCRMSync()` - Triggers `crm-user-sync` workflow
     - Input: userId, syncType, data

   **Email Workflows:**
   - `StartEmailCampaign()` - Triggers `email-campaign-workflow`
     - Input: campaignId, recipients, templateId
   
   - `StartWelcomeSeries()` - Triggers `welcome-email-series` workflow
     - Input: userId, email

   **Social Auth:**
   - `StartSocialAuthOrchestration()` - Triggers `social-auth-orchestration` workflow
     - Input: provider, authCode, redirectUri, metadata

   **Error Handling:**
   - `StartErrorHandling()` - Triggers `error-handling-workflow`
     - Input: errorCode, errorMessage, context

   **Admin Endpoints:**
   - `ListWorkflows()` - Lists active workflows (placeholder)
   - `GetWorkflowStatus()` - Gets workflow status by ID (placeholder)
   - `CancelWorkflow()` - Cancels workflow by ID (placeholder)

4. **Helper Methods:**

   **`startWorkflow()`:**
   - Creates Camunda workflow instance
   - Sets 30-second timeout
   - Builds create instance command
   - Adds variables from request
   - Sends command to Zeebe
   - Returns `WorkflowResponse` with instance key, process ID, status
   - Handles errors and logs appropriately

   **`getOrDefault()`:**
   - Returns value if not empty, otherwise returns default
   - Used for optional fields from JWT claims

   **Validation Helpers:**
   - `isValidSlug()` - Validates slug format (lowercase, numbers, hyphens)
   - `isValidEmail()` - Simple email validation (has @ and .)

5. **Response Format:**
   All workflow triggers return:
   ```json
   {
     "workflowInstanceKey": 12345,
     "processId": "ai_query",
     "requestId": "uuid-string",
     "status": "started",
     "message": "Workflow started successfully",
     "variables": {...}
   }
   ```

**Key Features:**
- **JWT Integration:** Extracts user context from JWT tokens
- **Request ID Generation:** Unique UUID for each request
- **Error Handling:** Comprehensive error handling and logging
- **Validation:** Input validation before workflow trigger
- **Admin Checks:** Role-based access control for admin operations

---

### 2. Franchise Handler (`internal/api/handlers/franchise_handler.go`)

**Purpose:** Handles direct franchise operations that don't require workflows (simple queries, user favorites, saved searches).

**Location:** `internal/api/handlers/franchise_handler.go`

**How It Works:**

1. **Handler Structure:**
   - Contains Elasticsearch client for search operations
   - Contains PostgreSQL client for user data
   - Contains logger for request logging
   - Helper methods for error responses

2. **Public Franchise Operations (No Auth Required):**

   **`SearchFranchises()`:**
   - Receives query parameters (query, category, location, investment range, etc.)
   - Validates and normalizes pagination (default: page=1, limit=10, max=100)
   - Determines Elasticsearch indices based on category:
     - "food" → `food_franchises`
     - "education" → `education_franchises`
     - "fashion" → `fashion_franchises`
     - No category → all indices
   - Builds Elasticsearch query using `buildSearchQuery()`
   - Executes search across indices
   - Processes results and extracts franchises
   - Returns paginated results with metadata

   **`GetByID()`:**
   - Receives franchise ID from URL parameter
   - Searches across all indices (food, education, fashion)
   - Returns first match found
   - Includes index metadata in response

   **`GetStats()`:**
   - Returns franchise statistics
   - Counts documents per category from Elasticsearch
   - Returns investment ranges and space requirements
   - Aggregates data from all indices

   **`GetSuggestions()`:**
   - Receives search query parameter
   - Builds Elasticsearch completion/suggest query
   - Searches across all indices
   - Extracts suggestions from response
   - Removes duplicates and limits to 10
   - Returns array of suggestion strings

   **`GetCategories()`:**
   - Returns hardcoded list of categories
   - Includes ID, name, description, icon for each category

   **`GetFeatured()`:**
   - Queries for featured franchises (featured=true)
   - Sorts by rating and popularity
   - Limits to 10 results
   - Searches across all indices

3. **User-Specific Operations (JWT Required):**

   **`AddToFavorites()`:**
   - Receives franchise ID from URL
   - Extracts user ID from JWT middleware
   - Inserts into `user_favorites` table
   - Uses `ON CONFLICT DO NOTHING` to prevent duplicates
   - Returns favorite ID

   **`RemoveFromFavorites()`:**
   - Receives franchise ID from URL
   - Extracts user ID from JWT
   - Deletes from `user_favorites` table
   - Returns success message

   **`GetFavorites()`:**
   - Extracts user ID from JWT
   - Queries `user_favorites` table for user's favorites
   - Fetches franchise details from Elasticsearch for each favorite
   - Returns array of franchise objects

   **`SaveSearch()`:**
   - Receives search name and filters in JSON body
   - Extracts user ID from JWT
   - Inserts into `saved_searches` table
   - Returns search ID

   **`GetSavedSearches()`:**
   - Extracts user ID from JWT
   - Queries `saved_searches` table
   - Returns array of saved searches with metadata

   **`DeleteSavedSearch()`:**
   - Receives search ID from URL
   - Extracts user ID from JWT
   - Deletes from `saved_searches` table
   - Validates ownership (user_id match)

4. **Helper Methods:**

   **`buildSearchQuery()`:**
   - Builds Elasticsearch query DSL from filters
   - Supports:
     - Text search (multi-match on brand, description, tags, highlights)
     - Category filtering (wildcard matching)
     - Location filtering (exact term match)
     - Investment range (range query)
     - Space range (range query)
     - Tags filtering (term queries)
     - Rating filtering (range query)
   - Adds pagination (from/size)
   - Adds sorting (by score, then rating)
   - Adds aggregations (by category, by location, investment stats)

   **`processSearchResults()`:**
   - Extracts hits from Elasticsearch response
   - Gets total count from response
   - Extracts franchise documents from hits
   - Adds Elasticsearch metadata (_id, _score, _index)
   - Returns franchises array and total count

   **`removeDuplicates()`:**
   - Removes duplicate strings from slice
   - Uses map for O(n) performance

   **Error Helpers:**
   - `validationError()` - Returns 400 Bad Request
   - `notFoundError()` - Returns 404 Not Found
   - `internalError()` - Returns 500 Internal Server Error with logging

5. **Database Tables Used:**
   - `user_favorites` - Stores user's favorite franchises
   - `saved_searches` - Stores user's saved search queries

6. **Elasticsearch Indices:**
   - `food_franchises` - Food & beverage franchises
   - `education_franchises` - Education franchises
   - `fashion_franchises` - Fashion franchises

**Key Features:**
- **Direct Database Access:** Bypasses workflows for simple queries
- **Multi-Index Search:** Searches across all franchise indices
- **User Context:** Extracts user ID from JWT for personalized operations
- **Error Handling:** Comprehensive error handling with appropriate HTTP status codes
- **Query Building:** Dynamic Elasticsearch query construction
- **Pagination:** Built-in pagination support
- **Aggregations:** Statistical aggregations for analytics

**Response Formats:**
- Search results include pagination metadata
- All responses follow consistent `{success, data, message}` format
- Error responses include error code and message

---

## Architecture Summary

### Request Flow Patterns

1. **Workflow-Based Operations:**
   ```
   HTTP Request → API Gateway → WorkflowHandler → Camunda Workflow → Workers → External Services
   ```

2. **Direct Handler Operations:**
   ```
   HTTP Request → API Gateway → FranchiseHandler → Database/Elasticsearch → Response
   ```

3. **Worker Processing:**
   ```
   Camunda Workflow → Worker Manager → Worker Handler → Business Logic → Complete Job
   ```

### Key Design Decisions

1. **Dual Architecture:**
   - Complex operations use workflows (orchestration, multi-step processes)
   - Simple queries use direct handlers (faster, less overhead)

2. **JWT-Based Authentication:**
   - All protected routes require valid JWT
   - User context extracted from JWT claims
   - Admin routes require admin role

3. **Configuration-Driven:**
   - Workers can be enabled/disabled via config
   - Timeouts and concurrency limits configurable
   - Environment-specific configurations

4. **Resilience:**
   - Retry logic with exponential backoff
   - Health checks for all dependencies
   - Graceful shutdown handling

5. **Observability:**
   - Structured logging with context
   - Prometheus metrics
   - Health check endpoints
   - Request ID tracking

---

## Development Workflow

1. **Add New Worker:**
   - Define activity in registry using `registry-updater`
   - Generate worker scaffold using `worker-generator`
   - Implement business logic in `service.go`
   - Add validation in `validation.go`
   - Write tests in `handler_test.go`
   - Register worker in `worker-manager/main.go`
   - Add configuration to `config.yaml`

2. **Add New API Endpoint:**
   - Determine if workflow-based or direct handler
   - Add route in `api-gateway/main.go`
   - Implement handler in appropriate handler file
   - Add authentication/authorization if needed
   - Test endpoint

3. **Deploy:**
   - Build binaries for worker-manager and api-gateway
   - Deploy with Docker or Kubernetes
   - Configure environment variables
   - Monitor health endpoints

---

## Support

For issues or questions:
- Check worker-specific README files in `docs/workers/`
- Review error logs with jobKey for debugging
- Consult architecture documentation in `docs/architecture.md`
- Review main.go files for initialization and registration logic
- Check API handlers for request/response formats

