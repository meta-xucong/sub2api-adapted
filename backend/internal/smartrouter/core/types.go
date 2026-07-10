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
}

type LaneSnapshot struct {
	LaneID                    string
	AccountID                 int64
	Name                      string
	SourceGroup               string
	Capabilities              map[Capability]bool
	ModelPatterns             []string
	Priority                  int
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
	Metadata                  map[string]string
}

type RouteResult struct {
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
	SkipReasons    map[string]string
}
