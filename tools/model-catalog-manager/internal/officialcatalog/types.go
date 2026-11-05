package officialcatalog

import (
	"net/http"
	"time"
)

const (
	SchemaVersion       = 1
	MaxInputReportBytes = 8 << 20
	MaxModelIDBytes     = 512
	MaxAccounts         = 5000
	MaxModelsPerAccount = 20000
	MaxTotalModels      = 50000
	MaxResponseBytes    = 2 << 20
	MaxPages            = 100
	MaxCredentialBytes  = 4096
)

type Options struct {
	AlibabaRegion    string
	AlibabaWorkspace string
}

type InputModel struct {
	AccountID         int64  `json:"account_id"`
	SourceFingerprint string `json:"source_fingerprint,omitempty"`
	Platform          string `json:"platform,omitempty"`
	AccountType       string `json:"account_type,omitempty"`
	APIProtocol       string `json:"api_protocol,omitempty"`
	ModelID           string `json:"model_id"`
	ObservedAs        string `json:"observed_as"`
}

// InputMetadata contains only explicitly whitelisted report-level fields.
// It is not part of the canonical whitelist digest used by official-review.
type InputMetadata struct {
	CreatedAt              string
	AccountScope           string
	AccountScanStatus      string
	AccountInventoryCount  int
	AccountInventorySHA256 string
	TargetBuildFingerprint string
}

type InputAccount struct {
	ID                int64
	SourceFingerprint string
	Platform          string
	AccountType       string
	APIProtocol       string
}

type MappingEdge struct {
	AccountID         int64
	SourceFingerprint string
	Platform          string
	AccountType       string
	APIProtocol       string
	PublicID          string
	UpstreamID        string
}

type DiscoveryRecord struct {
	AccountID         int64
	SourceFingerprint string
	Status            string
	LiveListAvailable bool
	Models            []string
}

type Inventory struct {
	Mode             string            `json:"mode"`
	Digest           string            `json:"digest"`
	Observations     []InputModel      `json:"observations"`
	Metadata         InputMetadata     `json:"-"`
	AccountRowIDs    []int64           `json:"-"`
	DiscoveryRowIDs  []int64           `json:"-"`
	Accounts         []InputAccount    `json:"-"`
	Mappings         []MappingEdge     `json:"-"`
	DiscoveryRecords []DiscoveryRecord `json:"-"`
}

type inputAccount struct {
	ID                int64          `json:"account_id"`
	SourceFingerprint string         `json:"source_fingerprint,omitempty"`
	Platform          string         `json:"platform,omitempty"`
	AccountType       string         `json:"account_type,omitempty"`
	APIProtocol       string         `json:"api_protocol,omitempty"`
	Mappings          []inputMapping `json:"mappings,omitempty"`
	DiscoveredModels  []string       `json:"discovered_models,omitempty"`
}

type inputMapping struct {
	PublicID   string `json:"public_id"`
	UpstreamID string `json:"upstream_id"`
}

type ProviderResult struct {
	Provider    string           `json:"provider"`
	Status      string           `json:"status"`
	Evidence    string           `json:"evidence"`
	RetrievedAt *time.Time       `json:"retrieved_at,omitempty"`
	ModelCount  int              `json:"model_count"`
	ErrorCode   string           `json:"error_code,omitempty"`
	Limitation  string           `json:"limitation"`
	Endpoints   []EndpointResult `json:"endpoints,omitempty"`
}

type EndpointResult struct {
	Endpoint    string     `json:"endpoint"`
	Status      string     `json:"status"`
	ModelCount  int        `json:"model_count"`
	ErrorCode   string     `json:"error_code,omitempty"`
	RetrievedAt *time.Time `json:"retrieved_at,omitempty"`
}

type Reference struct {
	Provider     string   `json:"provider"`
	ModelID      string   `json:"model_id"`
	MatchKind    string   `json:"match_kind"`
	Status       string   `json:"status"`
	Lifecycle    string   `json:"lifecycle,omitempty"`
	DeprecatedAt string   `json:"deprecated_at,omitempty"`
	RetiresAt    string   `json:"retires_at,omitempty"`
	ShutdownDate string   `json:"shutdown_date,omitempty"`
	OfflineTime  string   `json:"offline_time,omitempty"`
	Line         string   `json:"line,omitempty"`
	Endpoints    []string `json:"endpoints,omitempty"`
}

type Observation struct {
	AccountID         int64       `json:"account_id"`
	SourceFingerprint string      `json:"source_fingerprint,omitempty"`
	Platform          string      `json:"platform,omitempty"`
	AccountType       string      `json:"account_type,omitempty"`
	APIProtocol       string      `json:"api_protocol,omitempty"`
	ModelID           string      `json:"model_id"`
	ObservedAs        string      `json:"observed_as"`
	Status            string      `json:"status"`
	MultipleOEMRefs   bool        `json:"multiple_oem_references,omitempty"`
	Matches           []Reference `json:"official_references,omitempty"`
	Warning           string      `json:"warning"`
}

type NameShapeCandidate struct {
	AccountID      int64  `json:"account_id"`
	FullModelID    string `json:"full_model_id"`
	ObservedPrefix string `json:"observed_prefix"`
	Label          string `json:"label"`
	Action         string `json:"action"`
	Equivalence    string `json:"equivalence"`
}

type Report struct {
	SchemaVersion   int                  `json:"schema_version"`
	ReportID        string               `json:"report_id"`
	CreatedAt       time.Time            `json:"created_at"`
	InputReportMode string               `json:"input_report_mode"`
	InputDigest     string               `json:"input_whitelist_sha256"`
	ProviderResults []ProviderResult     `json:"providers"`
	Observations    []Observation        `json:"observations"`
	NameShape       []NameShapeCandidate `json:"name_shape_candidates"`
	GlobalWarning   string               `json:"warning"`
}

type modelRecord struct {
	Provider     string
	ModelID      string
	Status       string
	Lifecycle    string
	DeprecatedAt string
	RetiresAt    string
	ShutdownDate string
	OfflineTime  string
	Line         string
	Aliases      []string
	Endpoints    []string
}

type fetchedCatalog struct {
	Result ProviderResult
	Models []modelRecord
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
