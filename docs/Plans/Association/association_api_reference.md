# Association Module — Complete API Reference for Frontend

> **Base URL (Dev):** `https://us-dev-api.lemici.com`
> **Entity Type Param:** All `/entities/:entityType` routes use `associations` as the `:entityType` value.
> **Clean URL Pattern:** Frontend can call `/api/v1/association/...` — the gateway rewrites to `/api/v1/entities/associations/...` internally.

---

## 📑 Table of Contents

| # | Category | Auth |
|---|----------|------|
| 1 | [Home Page](#1-home-page) | ❌ No |
| 2 | [Listing Page](#2-listing-page) | ❌ No |
| 3 | [Detail Page (Public)](#3-detail-page-public) | ❌ No |
| 4 | [Full Detail (Private)](#4-full-detail-private) | ✅ Yes |
| 5 | [Search](#5-search) | ❌ No |
| 6 | [Autocomplete Suggest](#6-autocomplete-suggest) | ❌ No |
| 7 | [Industries](#7-industries) | ❌ No |
| 8 | [Categories](#8-categories) | ❌ No |
| 9 | [Get By ID](#9-get-by-id) | ❌ No |
| 10 | [Stats](#10-stats) | ❌ No |
| 11 | [Featured](#11-featured) | ❌ No |
| 12 | [Favorites (Like/Unlike)](#12-favorites-likeunlike) | ✅ Yes |
| 13 | [Bookmarks](#13-bookmarks) | ✅ Yes |
| 14 | [Ratings & Reviews](#14-ratings--reviews) | Mixed |
| 15 | [Share Tracking](#15-share-tracking) | ✅ Yes |
| 16 | [Enquiry Submission](#16-enquiry-submission) | ✅ Yes |
| 17 | [Onboarding (Registration)](#17-onboarding-registration) | ❌ No |
| 18 | [Document Upload (S3)](#18-document-upload-s3) | ✅ Yes |
| 19 | [Offerings](#19-offerings) | ✅ Yes |
| 20 | [Memberships](#20-memberships) | ✅ Yes |
| 21 | [Saved Searches](#21-saved-searches) | ✅ Yes |
| 22 | [Contact Us](#22-contact-us) | ❌ No |

---

## 1. Home Page

```
GET /api/v1/association/home
```

**Auth:** ❌ Not required
**Description:** Returns the association homepage layout with all sections.

**Response:**
```json
{
  "success": true,
  "data": {
    "pageId": "association_home",
    "sections": [
      {
        "type": "explore_by_industry",
        "enabled": true,
        "data": {
          "industries": [
            {
              "id": "uuid",
              "name": "Technology",
              "slug": "technology",
              "icon_url": "/AssociationImages/ExploreByIndustry/technology.svg"
            }
          ]
        }
      },
      {
        "type": "featured_business_associations",
        "enabled": true,
        "data": [
          {
            "id": "uuid",
            "association_name": "Kassia",
            "association_type": "Industry Body",
            "description": "...",
            "location": "Bengaluru,India",
            "year_of_establishment": "1949",
            "no_of_members": 12000,
            "MembershipFeeRange": {
              "FeeUnit": "INR",
              "minFee": null,
              "maxFee": null
            },
            "logo": {
              "alt": "",
              "url": "/AssociationImages/FeaturedAssociations/kassia.svg"
            },
            "tags": ["ISO 9001:2015 Certified", "Policy & Grievance Support"]
          }
        ]
      }
    ]
  },
  "metadata": {
    "generatedAt": "2026-06-15T21:54:48Z",
    "pageType": "home",
    "source": "workflow"
  }
}
```

---

## 2. Listing Page

```
GET /api/v1/association/listing
```

**Auth:** ❌ Not required
**Description:** Returns default paginated browse results for associations.

**Query Parameters:**
| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `page` | int | `1` | Page number |
| `limit` | int | `20` | Results per page |
| `industry` | string | — | Filter by industry slug |
| `location` | string | — | Filter by location/city |
| `sort_by` | string | — | Sort field |
| `sort_order` | string | `desc` | `asc` or `desc` |

**Response:** Same section-based structure. Key sections:
- `hero` — page title & description
- `business_associations` — array of association cards
- `functions_of_business_associations` — static functions list
- `statistics` — numbers
- `business_associations_across_india` — state-wise data
- `featured_business_categories` — category icons
- `category_questions` — FAQs
- `recommended_business_associations` — recommended icons
- `key_market_insights` — market data

**Association Card Shape (in listing/home):**
```json
{
  "id": "uuid",
  "association_name": "string",
  "association_type": "string",
  "description": "string",
  "location": "string",
  "year_of_establishment": "string",
  "no_of_members": "number|null",
  "MembershipFeeRange": {
    "FeeUnit": "INR",
    "minFee": "number|null",
    "maxFee": "number|null"
  },
  "logo": {
    "alt": "string",
    "url": "string"
  },
  "tags": ["string"]
}
```

---

## 3. Detail Page (Public)

```
GET /api/v1/association/detail/:slug
```

**Auth:** ❌ Not required
**Description:** Returns full public metadata for a single association by slug.

**Response sections (in order):**

| Section Type | Description |
|-------------|-------------|
| `association_hero_info_card` | Name, logo, description, likes, rating, review_count, tags, socialLinks |
| `association_info_grid` | association_name, association_type, sector, year, legal_status, headquarters, regional_presence, website, contact |
| `membership_section` | Membership details, Eligibility, Application process |
| `services_and_institutional_offerings` | Array of {title, description} |
| `programs_and_initiatives_section` | Grouped by category (Startup_Programs, Skill_Development, etc.) |
| `publications_section` | Industry_Reports, Policy_Papers, Whitepapers, Newsletters, etc. |
| `regional_structure_section` | Governance model, HQ, jurisdiction, chapters |
| `events_engagement_section` | Flagship_Events, Conferences, Workshops, Networking, Training |
| `compliance_policy_section` | Policy consultation, Govt representation, Standards |
| `partnership_affiliations_section` | Government, International, Academic, Industry affiliations |
| `awards_recognition_section` | Awards, Partnerships, Certifications, Media |
| `digital_presence_section` | Website, Member_Portal, Knowledge_Portal, Additional_Channels |
| `transparency_verification_section` | Document availability, governance transparency |

Each section has:
```json
{
  "type": "section_type_string",
  "enabled": true,
  "data": { ... }
}
```

---

## 4. Full Detail (Private)

```
GET /api/v1/association/full/:slug
```

**Auth:** ✅ Required (Bearer Token)
**Description:** Returns full detailed view including private/admin-only fields (documents, internal notes).

---

## 5. Search

```
GET /api/v1/association/search
```

**Auth:** ❌ Not required
**Description:** Advanced search with filters across Elasticsearch.

**Query Parameters:**
| Param | Type | Description |
|-------|------|-------------|
| `query` | string | Free-text search keyword |
| `category` | string | Category slug |
| `industry` | string | Industry slug |
| `location` | string | City/State filter |
| `min_investment` | float | Min investment amount |
| `max_investment` | float | Max investment amount |
| `min_size` | int | Min member count |
| `max_size` | int | Max member count |
| `min_fee` | float | Min membership fee |
| `max_fee` | float | Max membership fee |
| `page` | int | Page number (default: 1) |
| `limit` | int | Results per page (default: 20) |
| `sort_by` | string | Sort field (e.g., `name`, `member_count`) |
| `sort_order` | string | `asc` or `desc` |
| `isFeaturedOnly` | bool | Filter only featured |
| `isSponsoredOnly` | bool | Filter only sponsored |
| `localBrandsOnly` | bool | Filter local brands only |

**Response:**
```json
{
  "success": true,
  "data": {
    "results": [ /* array of association cards */ ],
    "total_count": 42,
    "query_time_ms": 15,
    "applied_filters": {
      "query": "technology",
      "industry": "technology",
      "page": 1,
      "limit": 20
    }
  }
}
```

---

## 6. Autocomplete Suggest

```
GET /api/v1/association/suggest
```

**Auth:** ❌ Not required
**Description:** Returns autocomplete suggestions for search typeahead.

**Query Parameters:**
| Param | Type | Description |
|-------|------|-------------|
| `q` | string | Search prefix text |

---

## 7. Industries

```
GET /api/v1/association/industries
```

**Auth:** ❌ Not required
**Description:** Returns all industries with icons.

```
GET /api/v1/association/industries/:slug
```

**Auth:** ❌ Not required
**Description:** Returns a specific industry by slug with its associations.

---

## 8. Categories

```
GET /api/v1/association/categories
```

**Auth:** ❌ Not required
**Description:** Returns all categories.

---

## 9. Get By ID

```
GET /api/v1/association/:id
```

**Auth:** ❌ Not required
**Description:** Returns a single association by UUID.

---

## 10. Stats

```
GET /api/v1/association/stats
```

**Auth:** ❌ Not required
**Description:** Returns platform-wide statistics for the association module.

---

## 11. Featured

```
GET /api/v1/association/featured
```

**Auth:** ❌ Not required
**Description:** Returns featured/promoted associations.

---

## 12. Favorites (Like/Unlike)

### Like an Association
```
POST /api/v1/association/favorite/:id
```
**Auth:** ✅ Required

**Response:**
```json
{
  "success": true,
  "id": "uuid",
  "message": "Added to favorites successfully"
}
```

### Unlike an Association
```
DELETE /api/v1/association/favorite/:id
```
**Auth:** ✅ Required

**Response:**
```json
{
  "success": true,
  "message": "Removed from favorites successfully"
}
```

### Get User's Favorites
```
GET /api/v1/association/favorites
```
**Auth:** ✅ Required

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": "uuid",
      "association_name": "Kassia",
      "slug": "kassia",
      "logo": "/AssociationImages/FeaturedAssociations/kassia.svg",
      "location": "Bengaluru,India",
      "favorited_at": "2026-06-15T22:00:30Z"
    }
  ]
}
```

---

## 13. Bookmarks

### Add Bookmark
```
POST /api/v1/association/:id/bookmark
```
**Auth:** ✅ Required

**Response:**
```json
{ "success": true, "message": "Association bookmarked successfully" }
```

### Remove Bookmark
```
DELETE /api/v1/association/:id/bookmark
```
**Auth:** ✅ Required

**Response:**
```json
{ "success": true, "message": "Bookmark removed successfully" }
```

### Check if Bookmarked
```
GET /api/v1/association/:id/bookmark/check
```
**Auth:** ✅ Required

**Response:**
```json
{ "success": true, "isBookmarked": true }
```

### Get All User Bookmarks
```
GET /api/v1/association/user/bookmarks
```
**Auth:** ✅ Required

---

## 14. Ratings & Reviews

### Submit Rating
```
POST /api/v1/association/:id/rate
```
**Auth:** ✅ Required

**Body:**
```json
{
  "rating": 4.5,
  "review": "Very helpful industry body for MSMEs."
}
```

**Response:**
```json
{ "success": true, "message": "Review submitted successfully" }
```

### Update Rating
```
PUT /api/v1/association/:id/rate
```
**Auth:** ✅ Required

**Body:** Same as submit.

### Delete Rating
```
DELETE /api/v1/association/:id/rate
```
**Auth:** ✅ Required

### Get My Rating
```
GET /api/v1/association/:id/my-rating
```
**Auth:** ✅ Required

### Get Public Reviews (No Auth)
```
GET /api/v1/association/:id/ratings
```
**Auth:** ❌ Not required

**Response:**
```json
{
  "success": true,
  "averageRating": 4.5,
  "totalReviews": 1,
  "reviews": [
    {
      "userName": "Jane Smith",
      "rating": 4.5,
      "review": "Very helpful industry body for MSMEs.",
      "created_at": "2026-06-14T15:24:00Z"
    }
  ]
}
```

---

## 15. Share Tracking

```
POST /api/v1/association/:id/share
```
**Auth:** ✅ Required

**Body:**
```json
{ "sharePlatform": "whatsapp" }
```

**Response:**
```json
{ "success": true, "message": "Share tracked successfully" }
```

### Get User Shares
```
GET /api/v1/association/user/shares
```
**Auth:** ✅ Required

---

## 16. Enquiry Submission

```
POST /api/v1/association/:id/enquiry
```
**Auth:** ✅ Required

**Body:**
```json
{
  "name": "Jane Doe",
  "email": "jane.doe@example.com",
  "phone": "+919999999999",
  "message": "Interested in ordinary membership. Please send the document requirements."
}
```

**Response:**
```json
{ "success": true, "message": "Enquiry submitted successfully" }
```

---

## 17. Onboarding (Registration)

```
POST /api/v1/onboarding/submit/association
```

**Auth:** ❌ Not required (Public)
**Description:** Submits a new association onboarding/registration request. Triggers the Camunda `supplier-onboarding` workflow. Admin reviews before going live.

**Body:**
```json
{
  "associationName": "Thane Belapur Industries Association",
  "contactEmail": "tbia@tbia.org",
  "memberCount": 3000,
  "membershipFeeMin": 5000,
  "membershipFeeMax": 15000,
  "association_type": "Industry Body",
  "overview": {
    "about": "Thane Belapur Industries Association was established to represent...",
    "key_functions": ["Corporate Community", "Maharashtra Industry", "Economic Growth"]
  },
  "contact_details": {
    "office_address": "P-14, MIDC, Rabale, Navi Mumbai, Maharashtra",
    "phone_number": "022-27691978",
    "email": "tbia@tbia.org",
    "website": "https://tbia.org"
  },
  "documents": [
    { "type": "GST", "url": "https://lemici-onboarding-docs.s3.amazonaws.com/uploads/12345_gst.pdf" },
    { "type": "PAN", "url": "https://lemici-onboarding-docs.s3.amazonaws.com/uploads/12345_pan.pdf" }
  ]
}
```

**Response:**
```json
{
  "success": true,
  "message": "Onboarding request submitted successfully and is under review."
}
```

---

## 18. Document Upload (S3)

### Get Pre-signed URL
```
GET /api/v1/documents/presigned-url?fileName=gst_certificate.pdf
```
**Auth:** ✅ Required

**Response:**
```json
{
  "success": true,
  "uploadUrl": "https://lemici-onboarding-docs.s3.amazonaws.com/uploads/...?AWSAccessKeyId=...&Signature=...",
  "fileUrl": "https://lemici-onboarding-docs.s3.amazonaws.com/uploads/12345_gst.pdf"
}
```

### Upload Flow:
1. Call `GET /documents/presigned-url?fileName=xxx` → get `uploadUrl` and `fileUrl`
2. `PUT` the binary file directly to `uploadUrl` (direct S3 upload, no backend)
3. Use `fileUrl` in the onboarding form submission body

---

## 19. Offerings

```
GET    /api/v1/association/:id/offerings        ← Get all offerings
POST   /api/v1/association/:id/offerings        ← Create offering
PATCH  /api/v1/association/offerings/:offeringId ← Update offering
DELETE /api/v1/association/offerings/:offeringId ← Delete offering
POST   /api/v1/association/offerings/:offeringId/publish   ← Publish
POST   /api/v1/association/offerings/:offeringId/unpublish ← Unpublish
POST   /api/v1/association/offerings/:offeringId/redeem    ← Redeem
```
**Auth:** ✅ All require authentication

---

## 20. Memberships

```
GET    /api/v1/association/:id/memberships              ← Get memberships
POST   /api/v1/association/:id/memberships              ← Request membership
PATCH  /api/v1/association/memberships/:membershipId     ← Update status
```
**Auth:** ✅ All require authentication

---

## 21. Saved Searches

```
POST   /api/v1/association/save-search       ← Save a search
GET    /api/v1/association/saved-searches     ← Get saved searches
DELETE /api/v1/association/saved-searches/:id ← Delete saved search
```
**Auth:** ✅ All require authentication

---

## 22. Contact Us

```
POST /api/v1/contact
```
**Auth:** ❌ Not required (Rate-limited: 50/window)

**Body:**
```json
{
  "firstName": "John",
  "lastName": "Doe",
  "email": "john@example.com",
  "phone": "+919999999999",
  "company": "ABC Corp",
  "message": "I want to list my association"
}
```

---

## 🔐 Authentication

All protected endpoints require a **Bearer JWT token** in the `Authorization` header:

```
Authorization: Bearer <jwt_token>
```

Session-based auth (cookie) is also supported via `SessionOrJWTAuth` middleware.

---

## 🗺️ Frontend → Backend Field Mapping (Cards)

| Frontend JSON Key | Backend API Path | Notes |
|---|---|---|
| `id` | `id` | UUID |
| `association_name` | `name` | Name of the Association |
| `description` | `short_description` or `description` | Use short for cards |
| `association_type` | `association_metadata.association_type` | "National", "Regional", etc. |
| `logo.url` | `logo.circle` or `logo.square` | Logo URL |
| `location` | `country` or joined cities | Region |
| `year_of_establishment` | `founded_year` | SmallInt |
| `no_of_members` | `member_count` | Integer |
| `MembershipFeeRange.minFee` | `membership_fee_min` | Decimal |
| `MembershipFeeRange.maxFee` | `membership_fee_max` | Decimal |
| `tags` | `association_metadata.overview.key_functions` | Derived values |

---

## 📦 Reference JSON Specs

Full response shape specs are available in the repo:
- **Home Page:** `docs/Plans/Association/association_home.json`
- **Listing Page:** `docs/Plans/Association/association_listing.json`
- **Individual Page:** `docs/Plans/Association/association_individual.json`
- **Actions/Interactions:** `docs/Plans/Association/association_actions.json`
- **Integration Guide:** `docs/Plans/Association/association_integration_guide.md`

---

## ⚠️ Important Notes

1. **Entity Type Routing:** All routes use `/api/v1/association/...` — gateway internally maps to `/api/v1/entities/associations/...`
2. **Rate Limiting:** Public submission endpoints (`contact`, `onboarding`) are rate-limited to 50 requests per window
3. **Image Paths:** All `icon_url` and `logo.url` values are relative paths — prepend your CDN/asset base URL
4. **Null Values:** `no_of_members`, `minFee`, `maxFee` can be `null` — handle gracefully
5. **Section Toggling:** Each section has an `enabled: boolean` flag — only render if `enabled === true`
