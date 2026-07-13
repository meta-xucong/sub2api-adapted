package core

type Capability string

const (
	CapabilityChat            Capability = "chat"
	CapabilityResponses       Capability = "responses"
	CapabilityImageGeneration Capability = "image_generation"
	CapabilityImageEdit       Capability = "image_edit"
	CapabilityEmbedding       Capability = "embedding"
)

type FailureClass string

const (
	FailureClientError        FailureClass = "client_error"
	FailureCapabilityError    FailureClass = "capability_error"
	FailurePayloadRejected    FailureClass = "payload_rejected"
	FailureContentRejected    FailureClass = "content_rejected"
	FailureAuthForbidden      FailureClass = "auth_forbidden"
	FailureTransientForbidden FailureClass = "transient_forbidden"
	FailureRateLimited        FailureClass = "rate_limited"
	FailureUpstream5xx        FailureClass = "upstream_5xx"
	FailureTimeout            FailureClass = "timeout"
	FailureCancelled          FailureClass = "cancelled"
	FailureUnknown            FailureClass = "unknown"
)

type RecoveryStage string

const (
	RecoveryNormal    RecoveryStage = "normal"
	RecoveryCooling   RecoveryStage = "cooling"
	RecoveryProbeDue  RecoveryStage = "probe_due"
	RecoveryWarming5  RecoveryStage = "warming_5"
	RecoveryWarming25 RecoveryStage = "warming_25"
)

type RouteRequest struct {
	RequestID            string
	GroupID              string
	Model                string
	Capability           Capability
	StickyLaneID         string
	PreviousResponseID   string
	ExcludedLaneIDs      map[string]struct{}
	ExcludedSourceGroups map[string]struct{}
	AttemptNumber        int
	NowUnix              int64
	Seed                 uint64
	// RemainingBudgetSeconds is the end-to-end budget left for this request.
	// The fields are intentionally request-scoped so chat routing is unaffected.
	RemainingBudgetSeconds     float64
	MinimumAttemptSeconds      float64
	FinalizationReserveSeconds float64
	// ImageSizeTier is the normalized requested output tier (1K, 2K, or 4K)
	// for explicit OpenAI Images sizes. It is empty for non-image and
	// implicit-size requests, preserving ordinary lane selection.
	ImageSizeTier string
}

type LaneSnapshot struct {
	LaneID       string
	AccountID    int64
	Name         string
	SourceGroup  string
	Capabilities map[Capability]bool
	// ImageSizeTiers optionally makes a lane specialized for explicit image
	// output tiers. An empty slice means the lane remains a generic fallback.
	ImageSizeTiers []string
	ModelPatterns  []string
	Priority       int
	// PriorityPenalty is a temporary, in-memory shift applied after the
	// configured priority. It must never be persisted as the account priority.
	PriorityPenalty           int
	CostMultiplier            float64
	BaseWeight                float64
	MaxConcurrency            int
	SourceGroupMaxConcurrency int
	CurrentConcurrency        int
	CurrentWaiting            int
	LoadRate                  int
	HealthScore               float64
	ErrorRateEWMA             float64
	LatencyEWMAms             float64
	CooldownUntilUnix         int64
	RecoveryStage             RecoveryStage
	RecentFailureStreak       int
	Metadata                  map[string]string
}

type RouteResult struct {
	Source         string
	LaneID         string
	AccountID      int64
	SourceGroup    string
	Capability     Capability
	Model          string
	Success        bool
	StatusCode     int
	ErrorClass     FailureClass
	FirstTokenMs   *int
	TotalLatencyMs int64
	ErrorSummary   string
}

type CandidateDecision struct {
	LaneID      string
	AccountID   int64
	SourceGroup string
	Score       float64
}

type RoutePlan struct {
	Candidates     []CandidateDecision
	OrderedLaneIDs []string
	AttemptBudget  int
	BudgetBlocked  bool
	SkipReasons    map[string]string
}
