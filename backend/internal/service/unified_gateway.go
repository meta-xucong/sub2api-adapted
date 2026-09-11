package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The unified gateway is deliberately isolated from the legacy gateway
// services.  A route must resolve to one concrete account before its price is
// frozen; billing never re-selects a provider after the upstream call.

var (
	ErrUnifiedGatewayRouteNotFound       = errors.New("unified gateway route not found")
	ErrUnifiedGatewayNoEligibleAccount   = errors.New("unified gateway has no eligible account")
	ErrUnifiedGatewaySnapshotNotFound    = errors.New("unified gateway price snapshot not found")
	ErrUnifiedGatewaySnapshotConflict    = errors.New("unified gateway price snapshot conflict")
	ErrUnifiedGatewaySnapshotInProgress  = errors.New("unified gateway request is already in progress")
	ErrUnifiedGatewayBalanceInsufficient = errors.New("unified gateway balance is insufficient")
	ErrUnifiedGatewayInvalidRequest      = errors.New("unified gateway request is invalid")
	ErrUnifiedGatewayUpstreamFailed      = errors.New("unified gateway upstream failed")
	ErrUnifiedGatewayUsageMissing        = errors.New("unified gateway upstream usage is missing")
	ErrUnifiedGatewaySettlementFailed    = errors.New("unified gateway settlement failed")
	ErrUnifiedGatewayUnauthorized        = errors.New("unified gateway unauthorized")
)

const (
	UnifiedGatewaySnapshotQuoted           UnifiedGatewaySnapshotStatus = "quoted"
	UnifiedGatewaySnapshotReserved         UnifiedGatewaySnapshotStatus = "reserved"
	UnifiedGatewaySnapshotCaptured         UnifiedGatewaySnapshotStatus = "captured"
	UnifiedGatewaySnapshotReleased         UnifiedGatewaySnapshotStatus = "released"
	UnifiedGatewaySnapshotSettlementFailed UnifiedGatewaySnapshotStatus = "settlement_failed"
)

type UnifiedGatewaySnapshotStatus string

// UnifiedGatewayRouteTarget is a public-model route.  It intentionally does
// not contain an account: account selection happens through explicit bindings
// so the selected account can be written into the price snapshot.
type UnifiedGatewayRouteTarget struct {
	ID               int64
	AccessGroupID    int64
	BillingLaneID    string
	PublicModel      string
	ProviderIdentity string
	UpstreamModel    string
	Endpoint         string
	PoolID           string
	BillingMode      string
	RateMode         UnifiedRateMode
	RateBasis        UnifiedRateBasis
	LaneRule         *UnifiedRateRule
	PoolRule         *UnifiedRateRule
	Enabled          bool
	Priority         int
}

// UnifiedGatewayAccountBinding is the only object allowed to bind an
// upstream account to a unified route.  Provider/model/endpoint overrides are
// useful when one lane contains accounts with different upstream identities.
type UnifiedGatewayAccountBinding struct {
	ID               int64
	RouteTargetID    int64
	AccountID        int64
	ProviderIdentity string
	UpstreamModel    string
	Endpoint         string
	AccountRule      *UnifiedRateRule
	Probe            *UnifiedProbeSnapshot
	Enabled          bool
	Priority         int
}

type UnifiedGatewayRouteSelection struct {
	Target  UnifiedGatewayRouteTarget
	Binding UnifiedGatewayAccountBinding
}

func (s UnifiedGatewayRouteSelection) ProviderIdentity() string {
	if value := strings.TrimSpace(s.Binding.ProviderIdentity); value != "" {
		return value
	}
	return strings.TrimSpace(s.Target.ProviderIdentity)
}

func (s UnifiedGatewayRouteSelection) UpstreamModel() string {
	if value := strings.TrimSpace(s.Binding.UpstreamModel); value != "" {
		return value
	}
	return strings.TrimSpace(s.Target.UpstreamModel)
}

func (s UnifiedGatewayRouteSelection) Endpoint() string {
	if value := strings.TrimSpace(s.Binding.Endpoint); value != "" {
		return value
	}
	return strings.TrimSpace(s.Target.Endpoint)
}

// UnifiedGatewayRouteCatalog is kept small so a persistent catalog can be
// added without changing the gateway state machine.  The in-memory catalog is
// used by the local simulator and tests.
type UnifiedGatewayRouteCatalog interface {
	Resolve(ctx context.Context, accessGroupID int64, publicModel, endpoint string) (UnifiedGatewayRouteSelection, error)
	List(ctx context.Context, accessGroupID int64, publicModel, endpoint string) ([]UnifiedGatewayRouteSelection, error)
}

type MemoryUnifiedGatewayRouteCatalog struct {
	mu            sync.RWMutex
	nextID        int64
	nextBindingID int64
	targets       map[int64]UnifiedGatewayRouteTarget
	bindings      map[int64][]UnifiedGatewayAccountBinding
}

func NewMemoryUnifiedGatewayRouteCatalog() *MemoryUnifiedGatewayRouteCatalog {
	return &MemoryUnifiedGatewayRouteCatalog{
		nextID:        1,
		nextBindingID: 1,
		targets:       make(map[int64]UnifiedGatewayRouteTarget),
		bindings:      make(map[int64][]UnifiedGatewayAccountBinding),
	}
}

func (c *MemoryUnifiedGatewayRouteCatalog) CreateTarget(target UnifiedGatewayRouteTarget) (int64, error) {
	if c == nil {
		return 0, ErrUnifiedGatewayInvalidRequest
	}
	if err := validateUnifiedGatewayTarget(target); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if target.ID == 0 {
		target.ID = c.nextID
		c.nextID++
	} else if target.ID >= c.nextID {
		c.nextID = target.ID + 1
	}
	if _, exists := c.targets[target.ID]; exists {
		return 0, fmt.Errorf("%w: route target id %d", ErrUnifiedGatewaySnapshotConflict, target.ID)
	}
	c.targets[target.ID] = target
	return target.ID, nil
}

func (c *MemoryUnifiedGatewayRouteCatalog) AddBinding(binding UnifiedGatewayAccountBinding) error {
	if c == nil {
		return ErrUnifiedGatewayInvalidRequest
	}
	if binding.AccountID <= 0 || binding.RouteTargetID <= 0 {
		return fmt.Errorf("%w: route target and account are required", ErrUnifiedGatewayInvalidRequest)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.targets[binding.RouteTargetID]; !exists {
		return ErrUnifiedGatewayRouteNotFound
	}
	for _, current := range c.bindings[binding.RouteTargetID] {
		if current.AccountID == binding.AccountID {
			return fmt.Errorf("%w: account %d is already bound to route %d", ErrUnifiedGatewaySnapshotConflict, binding.AccountID, binding.RouteTargetID)
		}
	}
	if binding.ID == 0 {
		binding.ID = c.nextBindingID
		c.nextBindingID++
	} else if binding.ID >= c.nextBindingID {
		c.nextBindingID = binding.ID + 1
	}
	c.bindings[binding.RouteTargetID] = append(c.bindings[binding.RouteTargetID], binding)
	return nil
}

func (c *MemoryUnifiedGatewayRouteCatalog) SetTargetEnabled(targetID int64, enabled bool) error {
	if c == nil {
		return ErrUnifiedGatewayInvalidRequest
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	target, ok := c.targets[targetID]
	if !ok {
		return ErrUnifiedGatewayRouteNotFound
	}
	target.Enabled = enabled
	c.targets[targetID] = target
	return nil
}

func (c *MemoryUnifiedGatewayRouteCatalog) Resolve(_ context.Context, accessGroupID int64, publicModel, endpoint string) (UnifiedGatewayRouteSelection, error) {
	if c == nil {
		return UnifiedGatewayRouteSelection{}, ErrUnifiedGatewayRouteNotFound
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var candidates []UnifiedGatewayRouteSelection
	matchedTarget := false
	for _, target := range c.targets {
		if !target.Enabled || target.AccessGroupID != accessGroupID || target.PublicModel != publicModel || (endpoint != "" && target.Endpoint != endpoint) {
			continue
		}
		matchedTarget = true
		bindings := append([]UnifiedGatewayAccountBinding(nil), c.bindings[target.ID]...)
		sort.SliceStable(bindings, func(i, j int) bool {
			if bindings[i].Priority != bindings[j].Priority {
				return bindings[i].Priority < bindings[j].Priority
			}
			return bindings[i].ID < bindings[j].ID
		})
		for _, binding := range bindings {
			if binding.Enabled {
				candidates = append(candidates, UnifiedGatewayRouteSelection{Target: target, Binding: binding})
				break
			}
		}
	}
	if len(candidates) == 0 {
		if matchedTarget {
			return UnifiedGatewayRouteSelection{}, ErrUnifiedGatewayNoEligibleAccount
		}
		return UnifiedGatewayRouteSelection{}, ErrUnifiedGatewayRouteNotFound
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Target.Priority != candidates[j].Target.Priority {
			return candidates[i].Target.Priority < candidates[j].Target.Priority
		}
		if candidates[i].Target.ID != candidates[j].Target.ID {
			return candidates[i].Target.ID < candidates[j].Target.ID
		}
		return candidates[i].Binding.ID < candidates[j].Binding.ID
	})
	return candidates[0], nil
}

func (c *MemoryUnifiedGatewayRouteCatalog) List(_ context.Context, accessGroupID int64, publicModel, endpoint string) ([]UnifiedGatewayRouteSelection, error) {
	if c == nil {
		return nil, ErrUnifiedGatewayRouteNotFound
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []UnifiedGatewayRouteSelection
	for _, target := range c.targets {
		if !target.Enabled || target.AccessGroupID != accessGroupID || (publicModel != "" && target.PublicModel != publicModel) || (endpoint != "" && target.Endpoint != endpoint) {
			continue
		}
		bindings := append([]UnifiedGatewayAccountBinding(nil), c.bindings[target.ID]...)
		sort.SliceStable(bindings, func(i, j int) bool {
			if bindings[i].Priority != bindings[j].Priority {
				return bindings[i].Priority < bindings[j].Priority
			}
			return bindings[i].ID < bindings[j].ID
		})
		for _, binding := range bindings {
			if binding.Enabled {
				out = append(out, UnifiedGatewayRouteSelection{Target: target, Binding: binding})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Target.Priority != out[j].Target.Priority {
			return out[i].Target.Priority < out[j].Target.Priority
		}
		if out[i].Target.ID != out[j].Target.ID {
			return out[i].Target.ID < out[j].Target.ID
		}
		return out[i].Binding.ID < out[j].Binding.ID
	})
	return out, nil
}

func validateUnifiedGatewayTarget(target UnifiedGatewayRouteTarget) error {
	if target.AccessGroupID <= 0 || strings.TrimSpace(target.BillingLaneID) == "" || strings.TrimSpace(target.PublicModel) == "" || strings.TrimSpace(target.ProviderIdentity) == "" || strings.TrimSpace(target.UpstreamModel) == "" || strings.TrimSpace(target.Endpoint) == "" || strings.TrimSpace(target.BillingMode) == "" || target.RateMode == "" || target.RateBasis == "" {
		return fmt.Errorf("%w: route target fields are incomplete", ErrUnifiedGatewayInvalidRequest)
	}
	return nil
}

// UnifiedGatewayPriceSnapshotRecord is the durable request-level wrapper
// around the immutable pricing snapshot.  The route selection is persisted
// with it, preventing a later account/pool change from changing history.
type UnifiedGatewayPriceSnapshotRecord struct {
	ID                int64
	APIKeyID          int64
	UserID            int64
	AccessGroupID     int64
	RequestID         string
	AttemptID         string
	Selection         UnifiedGatewayRouteSelection
	Snapshot          UnifiedRoutePriceSnapshot
	Status            UnifiedGatewaySnapshotStatus
	CreatedAt         time.Time
	FinalizedAt       time.Time
	MeasuredUnits     float64
	UserCharge        float64
	UpstreamRequestID string
	ResponseBody      []byte
	FailureMessage    string
}

type UnifiedGatewayPriceSnapshotStore interface {
	Create(ctx context.Context, record *UnifiedGatewayPriceSnapshotRecord) (*UnifiedGatewayPriceSnapshotRecord, error)
	Get(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string) (*UnifiedGatewayPriceSnapshotRecord, error)
	Finalize(ctx context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error
}

type MemoryUnifiedGatewayPriceSnapshotStore struct {
	mu      sync.Mutex
	nextID  int64
	records map[string]*UnifiedGatewayPriceSnapshotRecord
}

func NewMemoryUnifiedGatewayPriceSnapshotStore() *MemoryUnifiedGatewayPriceSnapshotStore {
	return &MemoryUnifiedGatewayPriceSnapshotStore{nextID: 1, records: make(map[string]*UnifiedGatewayPriceSnapshotRecord)}
}

func unifiedGatewaySnapshotKey(apiKeyID, userID, accessGroupID int64, requestID, attemptID string) string {
	return fmt.Sprintf("%d:%d:%d:%s\x00%s", apiKeyID, userID, accessGroupID, strings.TrimSpace(requestID), strings.TrimSpace(attemptID))
}

func (s *MemoryUnifiedGatewayPriceSnapshotStore) Create(_ context.Context, record *UnifiedGatewayPriceSnapshotRecord) (*UnifiedGatewayPriceSnapshotRecord, error) {
	if s == nil || record == nil || record.APIKeyID <= 0 || record.UserID <= 0 || record.AccessGroupID <= 0 || strings.TrimSpace(record.RequestID) == "" || strings.TrimSpace(record.AttemptID) == "" || record.Snapshot.Digest == "" {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	copyRecord := *record
	if copyRecord.Status == "" {
		copyRecord.Status = UnifiedGatewaySnapshotQuoted
	}
	if copyRecord.CreatedAt.IsZero() {
		copyRecord.CreatedAt = time.Now().UTC()
	}
	key := unifiedGatewaySnapshotKey(copyRecord.APIKeyID, copyRecord.UserID, copyRecord.AccessGroupID, copyRecord.RequestID, copyRecord.AttemptID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[key]; exists {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	copyRecord.ID = s.nextID
	s.nextID++
	s.records[key] = &copyRecord
	return unifiedGatewaySnapshotClone(&copyRecord), nil
}

func (s *MemoryUnifiedGatewayPriceSnapshotStore) Get(_ context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string) (*UnifiedGatewayPriceSnapshotRecord, error) {
	if s == nil {
		return nil, ErrUnifiedGatewaySnapshotNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[unifiedGatewaySnapshotKey(apiKeyID, userID, accessGroupID, requestID, attemptID)]
	if !ok {
		return nil, ErrUnifiedGatewaySnapshotNotFound
	}
	return unifiedGatewaySnapshotClone(record), nil
}

func (s *MemoryUnifiedGatewayPriceSnapshotStore) Finalize(_ context.Context, apiKeyID, userID, accessGroupID int64, requestID, attemptID string, status UnifiedGatewaySnapshotStatus, measuredUnits, userCharge float64, upstreamRequestID string, responseBody []byte, failureMessage string) error {
	if s == nil {
		return ErrUnifiedGatewaySnapshotNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[unifiedGatewaySnapshotKey(apiKeyID, userID, accessGroupID, requestID, attemptID)]
	if !ok {
		return ErrUnifiedGatewaySnapshotNotFound
	}
	if !unifiedGatewaySnapshotTransitionAllowed(record.Status, status) {
		if record.Status == status {
			return nil
		}
		return fmt.Errorf("%w: %s -> %s", ErrUnifiedGatewaySnapshotConflict, record.Status, status)
	}
	record.Status = status
	record.MeasuredUnits = measuredUnits
	record.UserCharge = userCharge
	record.UpstreamRequestID = strings.TrimSpace(upstreamRequestID)
	record.ResponseBody = append([]byte(nil), responseBody...)
	record.FailureMessage = strings.TrimSpace(failureMessage)
	record.FinalizedAt = time.Now().UTC()
	return nil
}

func unifiedGatewaySnapshotTransitionAllowed(from, to UnifiedGatewaySnapshotStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case UnifiedGatewaySnapshotQuoted:
		return to == UnifiedGatewaySnapshotReserved || to == UnifiedGatewaySnapshotReleased || to == UnifiedGatewaySnapshotSettlementFailed || to == UnifiedGatewaySnapshotCaptured
	case UnifiedGatewaySnapshotReserved:
		return to == UnifiedGatewaySnapshotCaptured || to == UnifiedGatewaySnapshotReleased || to == UnifiedGatewaySnapshotSettlementFailed
	default:
		return false
	}
}

func unifiedGatewaySnapshotClone(record *UnifiedGatewayPriceSnapshotRecord) *UnifiedGatewayPriceSnapshotRecord {
	if record == nil {
		return nil
	}
	clone := *record
	clone.ResponseBody = append([]byte(nil), record.ResponseBody...)
	return &clone
}

type UnifiedGatewayReserveCommand struct {
	Key    string
	UserID int64
	Amount float64
}

type UnifiedGatewayCaptureCommand struct {
	Key    string
	UserID int64
	Amount float64
}

type UnifiedGatewayLedgerResult struct {
	Applied    bool
	Amount     float64
	NewBalance float64
}

type UnifiedGatewayChargeLedger interface {
	Reserve(ctx context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error)
	Capture(ctx context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error)
	Refund(ctx context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error)
	Release(ctx context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error)
	Balance(ctx context.Context, userID int64) (float64, error)
}

type memoryUnifiedGatewayHold struct {
	UserID  int64
	Amount  float64
	Status  UnifiedGatewaySnapshotStatus
	Charged float64
}

type MemoryUnifiedGatewayChargeLedger struct {
	mu      sync.Mutex
	balance map[int64]float64
	holds   map[string]*memoryUnifiedGatewayHold
}

func NewMemoryUnifiedGatewayChargeLedger(initialBalances map[int64]float64) *MemoryUnifiedGatewayChargeLedger {
	balance := make(map[int64]float64, len(initialBalances))
	for userID, amount := range initialBalances {
		balance[userID] = amount
	}
	return &MemoryUnifiedGatewayChargeLedger{balance: balance, holds: make(map[string]*memoryUnifiedGatewayHold)}
}

func (l *MemoryUnifiedGatewayChargeLedger) Reserve(_ context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error) {
	if l == nil || command.UserID <= 0 || strings.TrimSpace(command.Key) == "" || !isFiniteNonNegative(command.Amount) {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if hold, exists := l.holds[command.Key]; exists {
		if hold.UserID != command.UserID || hold.Amount != command.Amount {
			return nil, ErrUnifiedGatewaySnapshotConflict
		}
		return &UnifiedGatewayLedgerResult{Applied: false, Amount: hold.Amount, NewBalance: l.balance[command.UserID]}, nil
	}
	if l.balance[command.UserID] < command.Amount {
		return nil, ErrUnifiedGatewayBalanceInsufficient
	}
	l.balance[command.UserID] -= command.Amount
	l.holds[command.Key] = &memoryUnifiedGatewayHold{UserID: command.UserID, Amount: command.Amount, Status: UnifiedGatewaySnapshotReserved}
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: l.balance[command.UserID]}, nil
}

func (l *MemoryUnifiedGatewayChargeLedger) Capture(_ context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error) {
	if l == nil || command.UserID <= 0 || strings.TrimSpace(command.Key) == "" || !isFiniteNonNegative(command.Amount) {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, exists := l.holds[command.Key]
	if !exists {
		if l.balance[command.UserID] < command.Amount {
			return nil, ErrUnifiedGatewayBalanceInsufficient
		}
		l.balance[command.UserID] -= command.Amount
		l.holds[command.Key] = &memoryUnifiedGatewayHold{UserID: command.UserID, Amount: 0, Status: UnifiedGatewaySnapshotCaptured, Charged: command.Amount}
		return &UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: l.balance[command.UserID]}, nil
	}
	if hold.UserID != command.UserID {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	if hold.Status == UnifiedGatewaySnapshotCaptured {
		return &UnifiedGatewayLedgerResult{Applied: false, Amount: hold.Charged, NewBalance: l.balance[command.UserID]}, nil
	}
	if hold.Status == UnifiedGatewaySnapshotReleased {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	delta := command.Amount - hold.Amount
	if delta > 0 {
		if l.balance[command.UserID] < delta {
			return nil, ErrUnifiedGatewayBalanceInsufficient
		}
		l.balance[command.UserID] -= delta
	} else if delta < 0 {
		l.balance[command.UserID] += -delta
	}
	hold.Status = UnifiedGatewaySnapshotCaptured
	hold.Charged = command.Amount
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: command.Amount, NewBalance: l.balance[command.UserID]}, nil
}

func (l *MemoryUnifiedGatewayChargeLedger) Refund(_ context.Context, command UnifiedGatewayCaptureCommand) (*UnifiedGatewayLedgerResult, error) {
	if l == nil || command.UserID <= 0 || strings.TrimSpace(command.Key) == "" {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, exists := l.holds[command.Key]
	if !exists {
		return &UnifiedGatewayLedgerResult{Applied: false, NewBalance: l.balance[command.UserID]}, nil
	}
	if hold.UserID != command.UserID {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	if hold.Status == UnifiedGatewaySnapshotReleased {
		return &UnifiedGatewayLedgerResult{Applied: false, NewBalance: l.balance[command.UserID]}, nil
	}
	amount := hold.Amount
	if hold.Status == UnifiedGatewaySnapshotCaptured {
		amount = hold.Charged
	}
	l.balance[command.UserID] += amount
	hold.Status = UnifiedGatewaySnapshotReleased
	hold.Charged = 0
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: amount, NewBalance: l.balance[command.UserID]}, nil
}

func (l *MemoryUnifiedGatewayChargeLedger) Release(_ context.Context, command UnifiedGatewayReserveCommand) (*UnifiedGatewayLedgerResult, error) {
	if l == nil || command.UserID <= 0 || strings.TrimSpace(command.Key) == "" {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, exists := l.holds[command.Key]
	if !exists {
		return &UnifiedGatewayLedgerResult{Applied: false, NewBalance: l.balance[command.UserID]}, nil
	}
	if hold.UserID != command.UserID {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	if hold.Status == UnifiedGatewaySnapshotReleased {
		return &UnifiedGatewayLedgerResult{Applied: false, NewBalance: l.balance[command.UserID]}, nil
	}
	if hold.Status == UnifiedGatewaySnapshotCaptured {
		return nil, ErrUnifiedGatewaySnapshotConflict
	}
	l.balance[command.UserID] += hold.Amount
	hold.Status = UnifiedGatewaySnapshotReleased
	return &UnifiedGatewayLedgerResult{Applied: true, Amount: hold.Amount, NewBalance: l.balance[command.UserID]}, nil
}

func (l *MemoryUnifiedGatewayChargeLedger) Balance(_ context.Context, userID int64) (float64, error) {
	if l == nil || userID <= 0 {
		return 0, ErrUnifiedGatewayInvalidRequest
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balance[userID], nil
}

type UnifiedGatewayRequest struct {
	RequestID      string
	AttemptID      string
	AccessGroupID  int64
	APIKeyID       int64
	UserID         int64
	PublicModel    string
	Endpoint       string
	EstimatedUnits float64
	RawBody        []byte
}

type UnifiedGatewayUpstreamResult struct {
	Delivered         bool
	MeasuredUnits     float64
	UpstreamRequestID string
	ResponseBody      []byte
}

type UnifiedGatewayUpstreamExecutor interface {
	Forward(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error)
}

type UnifiedGatewayUpstreamExecutorFunc func(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error)

func (f UnifiedGatewayUpstreamExecutorFunc) Forward(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	return f(ctx, selection, request)
}

type UnifiedGatewayExecutionResult struct {
	Record            *UnifiedGatewayPriceSnapshotRecord
	Charge            float64
	MeasuredUnits     float64
	ResponseBody      []byte
	UpstreamRequestID string
}

type UnifiedGateway struct {
	catalog   UnifiedGatewayRouteCatalog
	snapshots UnifiedGatewayPriceSnapshotStore
	ledger    UnifiedGatewayChargeLedger
	upstream  UnifiedGatewayUpstreamExecutor
	now       func() time.Time
}

func NewUnifiedGateway(catalog UnifiedGatewayRouteCatalog, snapshots UnifiedGatewayPriceSnapshotStore, ledger UnifiedGatewayChargeLedger, upstream UnifiedGatewayUpstreamExecutor) *UnifiedGateway {
	return &UnifiedGateway{catalog: catalog, snapshots: snapshots, ledger: ledger, upstream: upstream, now: func() time.Time { return time.Now().UTC() }}
}

func (g *UnifiedGateway) SetClock(now func() time.Time) {
	if g != nil && now != nil {
		g.now = now
	}
}

func (g *UnifiedGateway) Execute(ctx context.Context, request UnifiedGatewayRequest) (*UnifiedGatewayExecutionResult, error) {
	if g == nil || g.catalog == nil || g.snapshots == nil || g.ledger == nil || g.upstream == nil {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	if err := validateUnifiedGatewayRequest(request); err != nil {
		return nil, err
	}
	if existing, err := g.snapshots.Get(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID); err == nil {
		if existing.Status == UnifiedGatewaySnapshotCaptured {
			return unifiedGatewayExecutionFromRecord(existing), nil
		}
		if existing.Status == UnifiedGatewaySnapshotReleased {
			return unifiedGatewayExecutionFromRecord(existing), fmt.Errorf("%w: previous attempt was released", ErrUnifiedGatewayUpstreamFailed)
		}
		if existing.Status == UnifiedGatewaySnapshotSettlementFailed {
			return unifiedGatewayExecutionFromRecord(existing), ErrUnifiedGatewaySettlementFailed
		}
		return nil, ErrUnifiedGatewaySnapshotInProgress
	} else if !errors.Is(err, ErrUnifiedGatewaySnapshotNotFound) {
		return nil, err
	}

	selection, err := g.catalog.Resolve(ctx, request.AccessGroupID, request.PublicModel, request.Endpoint)
	if err != nil {
		return nil, err
	}
	now := g.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	pricing, err := ResolveUnifiedRoutePrice(UnifiedRoutePricingInput{
		RouteID:          selection.Target.ID,
		BillingLaneID:    selection.Target.BillingLaneID,
		AccountID:        selection.Binding.AccountID,
		ProviderIdentity: selection.ProviderIdentity(),
		PublicModel:      selection.Target.PublicModel,
		UpstreamModel:    selection.UpstreamModel(),
		Endpoint:         selection.Endpoint(),
		BillingMode:      selection.Target.BillingMode,
		RateMode:         selection.Target.RateMode,
		RateBasis:        selection.Target.RateBasis,
		Now:              now,
		Probe:            selection.Binding.Probe,
		LaneRule:         selection.Target.LaneRule,
		PoolRule:         selection.Target.PoolRule,
		AccountRule:      selection.Binding.AccountRule,
	})
	if err != nil {
		return nil, err
	}
	record := &UnifiedGatewayPriceSnapshotRecord{
		APIKeyID:      request.APIKeyID,
		UserID:        request.UserID,
		AccessGroupID: request.AccessGroupID,
		RequestID:     request.RequestID,
		AttemptID:     request.AttemptID,
		Selection:     selection,
		Snapshot:      pricing,
		Status:        UnifiedGatewaySnapshotQuoted,
		CreatedAt:     now,
	}
	created, err := g.snapshots.Create(ctx, record)
	if err != nil {
		if errors.Is(err, ErrUnifiedGatewaySnapshotConflict) {
			if existing, getErr := g.snapshots.Get(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID); getErr == nil && existing.Status == UnifiedGatewaySnapshotCaptured {
				return unifiedGatewayExecutionFromRecord(existing), nil
			}
		}
		return nil, err
	}

	reservationKey := unifiedGatewaySnapshotKey(request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID)
	estimatedCharge := pricing.Charge(request.EstimatedUnits, true)
	if _, err = g.ledger.Reserve(ctx, UnifiedGatewayReserveCommand{Key: reservationKey, UserID: request.UserID, Amount: estimatedCharge}); err != nil {
		_ = g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotReleased, 0, 0, "", nil, err.Error())
		return nil, err
	}
	if err = g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotReserved, request.EstimatedUnits, estimatedCharge, "", nil, ""); err != nil {
		_, _ = g.ledger.Release(ctx, UnifiedGatewayReserveCommand{Key: reservationKey, UserID: request.UserID})
		return nil, err
	}

	upstreamResult, upstreamErr := g.upstream.Forward(ctx, selection, request)
	if upstreamErr != nil || !upstreamResult.Delivered {
		failureMessage := "upstream did not deliver a successful result"
		if upstreamErr != nil {
			failureMessage = upstreamErr.Error()
		}
		_, releaseErr := g.ledger.Release(ctx, UnifiedGatewayReserveCommand{Key: reservationKey, UserID: request.UserID})
		finalizeErr := g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotReleased, upstreamResult.MeasuredUnits, 0, upstreamResult.UpstreamRequestID, nil, failureMessage)
		if releaseErr != nil || finalizeErr != nil {
			return nil, errors.Join(fmt.Errorf("%w: %s", ErrUnifiedGatewayUpstreamFailed, failureMessage), releaseErr, finalizeErr)
		}
		return nil, fmt.Errorf("%w: %s", ErrUnifiedGatewayUpstreamFailed, failureMessage)
	}

	units := upstreamResult.MeasuredUnits
	if units <= 0 && (pricing.RateBasis == UnifiedRateBasisPerRequest || pricing.BillingMode == string(BillingModePerRequest)) {
		units = 1
	}
	if !isFinitePositive(units) {
		_, releaseErr := g.ledger.Release(ctx, UnifiedGatewayReserveCommand{Key: reservationKey, UserID: request.UserID})
		finalizeErr := g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotReleased, units, 0, upstreamResult.UpstreamRequestID, nil, ErrUnifiedGatewayUsageMissing.Error())
		return nil, errors.Join(ErrUnifiedGatewayUsageMissing, releaseErr, finalizeErr)
	}
	charge := pricing.Charge(units, true)
	if _, err = g.ledger.Capture(ctx, UnifiedGatewayCaptureCommand{Key: reservationKey, UserID: request.UserID, Amount: charge}); err != nil {
		_, releaseErr := g.ledger.Release(ctx, UnifiedGatewayReserveCommand{Key: reservationKey, UserID: request.UserID})
		finalizeErr := g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotSettlementFailed, units, 0, upstreamResult.UpstreamRequestID, upstreamResult.ResponseBody, err.Error())
		return nil, errors.Join(ErrUnifiedGatewaySettlementFailed, err, releaseErr, finalizeErr)
	}
	if err = g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotCaptured, units, charge, upstreamResult.UpstreamRequestID, upstreamResult.ResponseBody, ""); err != nil {
		// A database error can be ambiguous: the UPDATE may have committed even
		// though the client saw an error.  Read the durable state before deciding
		// whether a refund is safe.  Never refund an already captured snapshot.
		latest, getErr := g.snapshots.Get(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID)
		if getErr == nil && latest.Status == UnifiedGatewaySnapshotCaptured {
			return unifiedGatewayExecutionFromRecord(latest), nil
		}
		if getErr == nil && (latest.Status == UnifiedGatewaySnapshotQuoted || latest.Status == UnifiedGatewaySnapshotReserved) {
			_, refundErr := g.ledger.Refund(ctx, UnifiedGatewayCaptureCommand{Key: reservationKey, UserID: request.UserID, Amount: charge})
			recoveryErr := g.snapshots.Finalize(ctx, request.APIKeyID, request.UserID, request.AccessGroupID, request.RequestID, request.AttemptID, UnifiedGatewaySnapshotSettlementFailed, units, 0, upstreamResult.UpstreamRequestID, upstreamResult.ResponseBody, err.Error())
			return nil, errors.Join(ErrUnifiedGatewaySettlementFailed, err, refundErr, recoveryErr)
		}
		return nil, errors.Join(ErrUnifiedGatewaySettlementFailed, err, getErr)
	}
	created.Status = UnifiedGatewaySnapshotCaptured
	created.MeasuredUnits = units
	created.UserCharge = charge
	created.ResponseBody = append([]byte(nil), upstreamResult.ResponseBody...)
	created.UpstreamRequestID = upstreamResult.UpstreamRequestID
	return &UnifiedGatewayExecutionResult{Record: created, Charge: charge, MeasuredUnits: units, ResponseBody: append([]byte(nil), upstreamResult.ResponseBody...), UpstreamRequestID: upstreamResult.UpstreamRequestID}, nil
}

func (g *UnifiedGateway) ListModels(ctx context.Context, accessGroupID int64) ([]UnifiedGatewayModel, error) {
	if g == nil || g.catalog == nil {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	selections, err := g.catalog.List(ctx, accessGroupID, "", "")
	if err != nil {
		return nil, err
	}
	type modelKey struct{ model, endpoint string }
	models := make(map[modelKey]*UnifiedGatewayModel)
	now := g.now()
	for _, selection := range selections {
		_, err := ResolveUnifiedRoutePrice(UnifiedRoutePricingInput{
			RouteID: selection.Target.ID, BillingLaneID: selection.Target.BillingLaneID, AccountID: selection.Binding.AccountID,
			ProviderIdentity: selection.ProviderIdentity(), PublicModel: selection.Target.PublicModel, UpstreamModel: selection.UpstreamModel(), Endpoint: selection.Endpoint(),
			BillingMode: selection.Target.BillingMode, RateMode: selection.Target.RateMode, RateBasis: selection.Target.RateBasis, Now: now,
			Probe: selection.Binding.Probe, LaneRule: selection.Target.LaneRule, PoolRule: selection.Target.PoolRule, AccountRule: selection.Binding.AccountRule,
		})
		if err != nil {
			continue
		}
		key := modelKey{selection.Target.PublicModel, selection.Endpoint()}
		model := models[key]
		if model == nil {
			model = &UnifiedGatewayModel{ID: selection.Target.PublicModel, Object: "model", OwnedBy: selection.ProviderIdentity(), Endpoint: selection.Endpoint()}
			models[key] = model
		}
		if !containsString(model.BillingLanes, selection.Target.BillingLaneID) {
			model.BillingLanes = append(model.BillingLanes, selection.Target.BillingLaneID)
		}
	}
	out := make([]UnifiedGatewayModel, 0, len(models))
	for _, model := range models {
		sort.Strings(model.BillingLanes)
		out = append(out, *model)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Endpoint < out[j].Endpoint
	})
	return out, nil
}

type UnifiedGatewayModel struct {
	ID           string   `json:"id"`
	Object       string   `json:"object"`
	OwnedBy      string   `json:"owned_by"`
	Endpoint     string   `json:"endpoint"`
	BillingLanes []string `json:"billing_lanes"`
}

func validateUnifiedGatewayRequest(request UnifiedGatewayRequest) error {
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.AttemptID) == "" || request.APIKeyID <= 0 || request.AccessGroupID <= 0 || request.UserID <= 0 || strings.TrimSpace(request.PublicModel) == "" || strings.TrimSpace(request.Endpoint) == "" || !isFiniteNonNegative(request.EstimatedUnits) {
		return fmt.Errorf("%w: request id, attempt, user, group, model, endpoint and non-negative estimate are required", ErrUnifiedGatewayInvalidRequest)
	}
	return nil
}

func unifiedGatewayExecutionFromRecord(record *UnifiedGatewayPriceSnapshotRecord) *UnifiedGatewayExecutionResult {
	if record == nil {
		return nil
	}
	return &UnifiedGatewayExecutionResult{
		Record:            unifiedGatewaySnapshotClone(record),
		Charge:            record.UserCharge,
		MeasuredUnits:     record.MeasuredUnits,
		ResponseBody:      append([]byte(nil), record.ResponseBody...),
		UpstreamRequestID: record.UpstreamRequestID,
	}
}

func isFiniteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// UnifiedGatewayPrincipal and the memory authenticator are intentionally
// generic.  Production wiring can adapt the existing APIKeyAuth middleware;
// the local simulator uses a static token map and never touches production
// API-key rows.
type UnifiedGatewayPrincipal struct {
	APIKeyID      int64
	UserID        int64
	AccessGroupID int64
}

type UnifiedGatewayAuthenticator interface {
	Authenticate(ctx context.Context, token string) (UnifiedGatewayPrincipal, error)
}

type MemoryUnifiedGatewayAuthenticator struct {
	Tokens map[string]UnifiedGatewayPrincipal
}

func (a MemoryUnifiedGatewayAuthenticator) Authenticate(_ context.Context, token string) (UnifiedGatewayPrincipal, error) {
	principal, ok := a.Tokens[strings.TrimSpace(token)]
	if !ok || principal.UserID <= 0 || principal.AccessGroupID <= 0 {
		return UnifiedGatewayPrincipal{}, ErrUnifiedGatewayUnauthorized
	}
	return principal, nil
}

// UnifiedGatewaySimulationHandler is a local, opt-in HTTP harness.  It is
// useful for proving route selection and accounting without registering a
// route in the production router or calling a real provider.
type UnifiedGatewaySimulationHandler struct {
	Gateway       *UnifiedGateway
	Authenticator UnifiedGatewayAuthenticator
}

type unifiedGatewaySimulationRequest struct {
	Model           string  `json:"model"`
	EstimatedUnits  float64 `json:"estimated_units"`
	MeasuredUnits   float64 `json:"measured_units"`
	SimulateFailure bool    `json:"simulate_failure"`
}

func (h *UnifiedGatewaySimulationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Gateway == nil || h.Authenticator == nil {
		http.Error(w, "unified gateway is not configured", http.StatusNotImplemented)
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-API-Key"))
	}
	principal, err := h.Authenticator.Authenticate(r.Context(), token)
	if err != nil {
		writeUnifiedGatewayJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"type": "authentication_error", "message": "invalid API key"}})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		models, err := h.Gateway.ListModels(r.Context(), principal.AccessGroupID)
		if err != nil {
			writeUnifiedGatewayJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": err.Error()}})
			return
		}
		writeUnifiedGatewayJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
		return
	}
	if r.Method != http.MethodPost {
		writeUnifiedGatewayJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "not found"}})
		return
	}
	endpoint := map[string]string{
		"/v1/chat/completions":   "chat_completions",
		"/v1/images/generations": "images_generations",
		"/v1/videos":             "videos",
	}[r.URL.Path]
	if endpoint == "" {
		writeUnifiedGatewayJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "not found"}})
		return
	}
	var body unifiedGatewaySimulationRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeUnifiedGatewayJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "invalid JSON body"}})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		writeUnifiedGatewayJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "model is required"}})
		return
	}
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestID == "" {
		requestID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if body.EstimatedUnits < 0 || body.MeasuredUnits < 0 {
		writeUnifiedGatewayJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "units must be non-negative"}})
		return
	}
	rawBody, _ := json.Marshal(body)
	execution, err := h.Gateway.Execute(r.Context(), UnifiedGatewayRequest{
		RequestID: requestID, AttemptID: "1", AccessGroupID: principal.AccessGroupID, APIKeyID: principal.APIKeyID, UserID: principal.UserID,
		PublicModel: model, Endpoint: endpoint, EstimatedUnits: body.EstimatedUnits, RawBody: rawBody,
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrUnifiedGatewayBalanceInsufficient) {
			status = http.StatusPaymentRequired
		} else if errors.Is(err, ErrUnifiedGatewayRouteNotFound) || errors.Is(err, ErrUnifiedGatewayNoEligibleAccount) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrUnifiedGatewayInvalidRequest) {
			status = http.StatusBadRequest
		}
		writeUnifiedGatewayJSON(w, status, map[string]any{"error": map[string]any{"message": err.Error()}})
		return
	}
	response := map[string]any{
		"id":      execution.UpstreamRequestID,
		"object":  "chat.completion",
		"model":   model,
		"choices": []any{},
		"usage":   map[string]any{"total_units": execution.MeasuredUnits},
		"billing": map[string]any{"charge": execution.Charge, "route_id": execution.Record.Snapshot.RouteID, "lane": execution.Record.Snapshot.BillingLaneID, "provider": execution.Record.Snapshot.ProviderIdentity, "rate_source": execution.Record.Snapshot.RateSource},
	}
	if len(execution.ResponseBody) > 0 {
		var upstreamBody map[string]any
		if json.Unmarshal(execution.ResponseBody, &upstreamBody) == nil {
			response = upstreamBody
		}
	}
	writeUnifiedGatewayJSON(w, http.StatusOK, response)
}

func writeUnifiedGatewayJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
