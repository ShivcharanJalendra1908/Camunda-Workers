package models

// AssociationMetadata represents the standardized JSON structure for Association specific data.
type AssociationMetadata struct {
	AssociationType             string                       `json:"association_type,omitempty"`
	Overview                    *Overview                    `json:"overview,omitempty"`
	Governance                  *Governance                  `json:"governance,omitempty"`
	MembershipDetails           *MembershipDetails          `json:"membership_details,omitempty"`
	Eligibility                 *EligibilityCriteria        `json:"eligibility_criteria,omitempty"`
	ServicesOffered             []ServiceOffering           `json:"services_offered,omitempty"`
	ProgramsAndInitiatives     []ProgramInitiative         `json:"programs_and_initiatives,omitempty"`
	Publications               []Publication               `json:"publications,omitempty"`
	Events                      []Event                      `json:"events,omitempty"`
	GovernmentAndPolicy        *GovernmentAndPolicy        `json:"government_and_policy,omitempty"`
	Recognitions               *Recognitions               `json:"recognitions,omitempty"`
	TransparencyAndVerification []TransparencyItem          `json:"transparency_and_verification,omitempty"`
	DigitalPresence            []DigitalPresenceItem       `json:"digital_presence,omitempty"`
	RegionalStructure          *RegionalStructure          `json:"regional_structure,omitempty"`
	DataAndInsights            *DataAndInsights            `json:"data_and_insights,omitempty"`
	ContactDetails              *ContactDetails              `json:"contact_details,omitempty"`
	FAQs                        []FAQ                       `json:"faqs,omitempty"`
	Careers                     []JobOpening                `json:"careers,omitempty"`
	Tenders                     []TenderOpportunity         `json:"tenders,omitempty"`
}

type Overview struct {
	About        string   `json:"about,omitempty"`
	Mission      string   `json:"mission,omitempty"`
	Vision       string   `json:"vision,omitempty"`
	Objectives   []string `json:"objectives,omitempty"`
	KeyFunctions []string `json:"key_functions,omitempty"`
}

type Leader struct {
	Name     string `json:"name"`
	Company  string `json:"company,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type Council struct {
	Name    string `json:"name"`
	Company string `json:"company,omitempty"`
	Role    string `json:"role,omitempty"`
}

type PastPresident struct {
	Name        string `json:"name"`
	YearsActive string `json:"years_active,omitempty"`
}

type Governance struct {
	President               *Leader         `json:"president,omitempty"`
	VicePresidents          []Leader        `json:"vice_presidents,omitempty"`
	SecretaryGeneral        *Leader         `json:"secretary_general,omitempty"`
	Treasurer               *Leader         `json:"treasurer,omitempty"`
	GoverningCouncilMembers []Council       `json:"governing_council_members,omitempty"`
	AdvisoryBoard           []Council       `json:"advisory_board,omitempty"`
	PastPresidents          []PastPresident `json:"past_presidents,omitempty"`
}

type MetricKV struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type MembershipCategory struct {
	Name               string `json:"name"`
	AdmissionFee       string `json:"admission_fee,omitempty"`
	AnnualSubscription string `json:"annual_subscription,omitempty"`
	Description        string `json:"description,omitempty"`
}

type MembershipDetails struct {
	OverviewMetrics     []MetricKV           `json:"overview_metrics,omitempty"`
	Categories          []MembershipCategory `json:"categories,omitempty"`
	Benefits            []string             `json:"benefits,omitempty"`
	ModeOfApplication   string               `json:"mode_of_application,omitempty"`
	ApplicationURL      string               `json:"application_url,omitempty"`
	FormDownloadURL     string               `json:"form_download_url,omitempty"`
	MemberLoginURL      string               `json:"member_login_url,omitempty"`
	MemberDirectoryURL  string               `json:"member_directory_url,omitempty"`
	ApplicationProcess  []string             `json:"application_process,omitempty"`
}

type EligibilityCriteria struct {
	AllowedEntityTypes []string `json:"allowed_entity_types,omitempty"`
	RequiredSectors    []string `json:"required_sectors,omitempty"`
	DocumentaryProofs  []string `json:"documentary_proofs,omitempty"`
	GeneralRules       []string `json:"general_rules,omitempty"`
}

type ServiceOffering struct {
	Category    string `json:"category,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type ProgramInitiative struct {
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type Publication struct {
	Title         string `json:"title"`
	Category      string `json:"category,omitempty"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url,omitempty"`
	PublishedDate string `json:"published_date,omitempty"`
}

type Event struct {
	Name        string   `json:"name"`
	Category    string   `json:"category,omitempty"`
	Description string   `json:"description,omitempty"`
	StartDate   string   `json:"start_date,omitempty"`
	Location    string   `json:"location,omitempty"`
	AGMDetails  string   `json:"agm_details,omitempty"`
	Gallery     []string `json:"gallery,omitempty"`
}

type Scheme struct {
	Name    string `json:"name"`
	Details string `json:"details,omitempty"`
	URL     string `json:"url,omitempty"`
}

type NotificationCircular struct {
	Title string `json:"title"`
	Date  string `json:"date,omitempty"`
	URL   string `json:"url,omitempty"`
}

type MoU struct {
	Organization string `json:"organization"`
	Purpose      string `json:"purpose,omitempty"`
	Date         string `json:"date,omitempty"`
}

type GovernmentAndPolicy struct {
	SchemesSupported        []Scheme               `json:"schemes_supported,omitempty"`
	NotificationsCirculars  []NotificationCircular `json:"notifications_circulars,omitempty"`
	PolicyAdvocacyPapers    []NotificationCircular `json:"policy_advocacy_papers,omitempty"`
	MoUsWithGovt            []MoU                  `json:"mous_with_govt,omitempty"`
	RepresentationCommittees []string               `json:"representation_committees,omitempty"`
}

type Recognitions struct {
	Awards         []Award         `json:"awards,omitempty"`
	Certifications []Certification `json:"certifications,omitempty"`
	MediaMentions  []MediaMention  `json:"media_mentions,omitempty"`
}

type Award struct {
	Name        string `json:"name"`
	Year        int    `json:"year,omitempty"`
	Description string `json:"description,omitempty"`
}

type Certification struct {
	Name   string `json:"name"`
	Issuer string `json:"issuer,omitempty"`
	Year   int    `json:"year,omitempty"`
}

type MediaMention struct {
	Title     string `json:"title"`
	Publisher string `json:"publisher,omitempty"`
	URL       string `json:"url,omitempty"`
}

type TransparencyItem struct {
	Category    string `json:"category"`
	ItemName    string `json:"item_name"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
}

type DigitalPresenceItem struct {
	Category     string `json:"category"`
	PropertyName string `json:"property_name"`
	Status       string `json:"status"`
	Description  string `json:"description,omitempty"`
}

type RegionalStructure struct {
	GovernanceModel string    `json:"governance_model,omitempty"`
	Headquarters    string    `json:"headquarters,omitempty"`
	RegionalOffices string    `json:"regional_offices,omitempty"`
	Jurisdiction    string    `json:"jurisdiction,omitempty"`
	MapURL          string    `json:"map_url,omitempty"`
	Chapters        []Chapter `json:"chapters,omitempty"`
}

type Chapter struct {
	Name                string `json:"name"`
	HeadquartersAddress string `json:"headquarters_address,omitempty"`
	ContactEmail        string `json:"contact_email,omitempty"`
}

type DataAndInsights struct {
	MarketStats  string   `json:"market_stats,omitempty"`
	SectorTrends []string `json:"sector_trends,omitempty"`
	EximData     string   `json:"exim_data,omitempty"`
	ClusterInfo  string   `json:"cluster_info,omitempty"`
}

type ContactDetails struct {
	OfficeAddress string      `json:"office_address,omitempty"`
	PhoneNumber   string      `json:"phone_number,omitempty"`
	Email         string      `json:"email,omitempty"`
	SocialLinks   SocialLinks `json:"social_links,omitempty"`
}

type SocialLinks struct {
	Linkedin string `json:"linkedin,omitempty"`
	Twitter  string `json:"twitter,omitempty"`
	Facebook string `json:"facebook,omitempty"`
	Youtube  string `json:"youtube,omitempty"`
}

type FAQ struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type JobOpening struct {
	Title    string `json:"title"`
	Location string `json:"location,omitempty"`
	URL      string `json:"url,omitempty"`
}

type TenderOpportunity struct {
	Title    string `json:"title"`
	Deadline string `json:"deadline,omitempty"`
}
