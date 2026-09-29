package service

import (
	"context"
	"time"

	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
)

// SmartRouterHealthState is the durable projection used to restore routing
// behavior after a container restart. It never contains credentials, prompts,
// request bodies, or generated images.
type SmartRouterHealthState struct {
	LaneID          string
	AccountID       int64
	SourceGroup     string
	Capability      smartrouter.Capability
	ModelFamily     string
	Snapshot        smartrouter.HealthSnapshot
	LastSuccessAt   time.Time
	LastFailureAt   time.Time
	LastFailureUnix int64
}

// SmartRouterCapabilityEvidence is the compact ledger view that decides which
// daily probes are necessary for a lane.
type SmartRouterCapabilityEvidence struct {
	LaneID                     string
	ChatKnown                  bool
	ChatLastSuccess            time.Time
	ChatLastFailure            time.Time
	ChatRecoveryPriority       int
	ResponsesKnown             bool
	ResponsesLastSuccess       time.Time
	ResponsesLastFailure       time.Time
	ResponsesRecoveryPriority  int
	GenerationKnown            bool
	EditKnown                  bool
	GenerationLastSuccess      time.Time
	GenerationLastFailure      time.Time
	GenerationRecoveryPriority int
	EditLastSuccess            time.Time
	EditLastFailure            time.Time
	EditRecoveryPriority       int
	CompactKnown               bool
	CompactLastSuccess         time.Time
	CompactLastFailure         time.Time
	CompactRecoveryPriority    int
}

type SmartRouterCalibrationRun struct {
	ID           int64
	ScheduledFor time.Time
	StartedAt    time.Time
}

type SmartRouterCalibrationResult struct {
	LaneID       string
	AccountID    int64
	SourceGroup  string
	Capability   smartrouter.Capability
	ModelFamily  string
	Success      bool
	StatusCode   int
	LatencyMs    int64
	ErrorSummary string
	Reason       string
}

// SmartRouterHealthLedger is implemented by the persistence layer. A nil
// ledger keeps Smart Router fully backward compatible and memory-only.
type SmartRouterHealthLedger interface {
	LoadStates(ctx context.Context) ([]SmartRouterHealthState, error)
	RecordEvent(ctx context.Context, event smartrouter.HealthEvent) error
	ListCapabilityEvidence(ctx context.Context) ([]SmartRouterCapabilityEvidence, error)
	BeginCalibrationRun(ctx context.Context, scheduledFor time.Time) (*SmartRouterCalibrationRun, bool, error)
	RecordCalibrationResult(ctx context.Context, runID int64, result SmartRouterCalibrationResult) error
	FinishCalibrationRun(ctx context.Context, runID int64, success bool, summary string) error
}
