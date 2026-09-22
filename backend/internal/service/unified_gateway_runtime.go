package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

var (
	ErrUnifiedGatewayRuntimeDisabled        = errors.New("unified gateway runtime is disabled")
	ErrUnifiedGatewayRuntimeUnsupported     = errors.New("unified gateway runtime operation is unsupported")
	ErrUnifiedGatewayRuntimeRequestNotFound = errors.New("unified gateway asynchronous request not found")
)

// UnifiedGatewayAsyncUpstream is optional. Providers that accept an async
// job implement Poll; providers that are strictly synchronous only implement
// UnifiedGatewayUpstreamExecutor.
type UnifiedGatewayAsyncUpstream interface {
	Poll(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayUpstreamResult, error)
}

// UnifiedGatewayContentUpstream is optional and is used for media download
// after the job snapshot has already been captured.
type UnifiedGatewayContentUpstream interface {
	Content(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayContentResult, error)
}

// UnifiedGatewayRouteEligibility lets an upstream adapter reject a concrete
// account before a price snapshot is created. It is advisory: the immutable
// catalog and pricing checks remain the final gate.
type UnifiedGatewayRouteEligibility interface {
	Eligible(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (bool, error)
}

type UnifiedGatewayRuntimeRequest struct {
	RequestID      string
	AttemptID      string
	APIKeyID       int64
	UserID         int64
	AccessGroupID  int64
	PublicModel    string
	Endpoint       string
	EstimatedUnits float64
	RawBody        []byte
}

type UnifiedGatewayRuntimeResponse struct {
	StatusCode        int
	ContentType       string
	Headers           http.Header
	Body              []byte
	Pending           bool
	Charge            float64
	MeasuredUnits     float64
	UpstreamRequestID string
	Record            *UnifiedGatewayPriceSnapshotRecord
}

type unifiedGatewayPendingRequest struct {
	Request UnifiedGatewayRequest
}

// UnifiedGatewayRuntimeService is the isolated production runtime. It owns
// candidate selection and async lookup, while UnifiedGateway owns the
// reserve/capture/release state machine. No legacy group/channel billing
// service is called from this type.
type UnifiedGatewayRuntimeService struct {
	gateway   *UnifiedGateway
	catalog   UnifiedGatewayRouteCatalog
	snapshots UnifiedGatewayPriceSnapshotStore
	upstream  UnifiedGatewayUpstreamExecutor
	cfg       *config.Config

	mu      sync.RWMutex
	pending map[string]unifiedGatewayPendingRequest
}

func NewUnifiedGatewayRuntimeService(
	gateway *UnifiedGateway,
	catalog UnifiedGatewayRouteCatalog,
	snapshots UnifiedGatewayPriceSnapshotStore,
	upstream UnifiedGatewayUpstreamExecutor,
	cfg *config.Config,
) *UnifiedGatewayRuntimeService {
	return &UnifiedGatewayRuntimeService{
		gateway:   gateway,
		catalog:   catalog,
		snapshots: snapshots,
		upstream:  upstream,
		cfg:       cfg,
		pending:   make(map[string]unifiedGatewayPendingRequest),
	}
}

func (s *UnifiedGatewayRuntimeService) enabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.UnifiedGatewayRuntimeEnabled
}

func (s *UnifiedGatewayRuntimeService) accessGroupAllowed(accessGroupID int64) bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.UnifiedGatewayAccessGroupID > 0 && accessGroupID == s.cfg.Gateway.UnifiedGatewayAccessGroupID
}

func (s *UnifiedGatewayRuntimeService) ensureReady() error {
	if s == nil || s.gateway == nil || s.gateway.accountReader == nil || s.catalog == nil || s.snapshots == nil || s.upstream == nil {
		return ErrUnifiedGatewayRuntimeUnsupported
	}
	if !s.enabled() {
		return ErrUnifiedGatewayRuntimeDisabled
	}
	return nil
}

func (s *UnifiedGatewayRuntimeService) Execute(ctx context.Context, input UnifiedGatewayRuntimeRequest) (*UnifiedGatewayRuntimeResponse, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.RequestID) == "" {
		input.RequestID = uuid.NewString()
	}
	if strings.TrimSpace(input.AttemptID) == "" {
		input.AttemptID = "public"
	}
	if input.EstimatedUnits < 0 || input.APIKeyID <= 0 || input.UserID <= 0 || input.AccessGroupID <= 0 || strings.TrimSpace(input.PublicModel) == "" || strings.TrimSpace(input.Endpoint) == "" {
		return nil, ErrUnifiedGatewayInvalidRequest
	}
	if !s.accessGroupAllowed(input.AccessGroupID) {
		return nil, ErrUnifiedGatewayUnauthorized
	}
	candidates, err := s.catalog.List(ctx, input.AccessGroupID, strings.TrimSpace(input.PublicModel), strings.TrimSpace(input.Endpoint))
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrUnifiedGatewayRouteNotFound
	}
	// Catalogs are ordered by priority. Re-sort defensively so an alternate
	// catalog implementation cannot change dispatch order accidentally.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Target.Priority != candidates[j].Target.Priority {
			return candidates[i].Target.Priority < candidates[j].Target.Priority
		}
		if candidates[i].Binding.Priority != candidates[j].Binding.Priority {
			return candidates[i].Binding.Priority < candidates[j].Binding.Priority
		}
		if candidates[i].Target.ID != candidates[j].Target.ID {
			return candidates[i].Target.ID < candidates[j].Target.ID
		}
		return candidates[i].Binding.ID < candidates[j].Binding.ID
	})

	var lastErr error
	for index := range candidates {
		selection := candidates[index]
		request := UnifiedGatewayRequest{
			RequestID:         strings.TrimSpace(input.RequestID),
			AttemptID:         fmt.Sprintf("%s-route-%d-%d", strings.TrimSpace(input.AttemptID), selection.Target.ID, selection.Binding.ID),
			AccessGroupID:     input.AccessGroupID,
			APIKeyID:          input.APIKeyID,
			UserID:            input.UserID,
			PublicModel:       strings.TrimSpace(input.PublicModel),
			Endpoint:          strings.TrimSpace(input.Endpoint),
			EstimatedUnits:    input.EstimatedUnits,
			RawBody:           append([]byte(nil), input.RawBody...),
			SelectionOverride: &selection,
		}
		if eligibility, ok := s.upstream.(UnifiedGatewayRouteEligibility); ok {
			eligible, eligibilityErr := eligibility.Eligible(ctx, selection, request)
			if eligibilityErr != nil {
				lastErr = eligibilityErr
				continue
			}
			if !eligible {
				lastErr = ErrUnifiedGatewayNoEligibleAccount
				continue
			}
		}
		result, executeErr := s.gateway.Execute(ctx, request)
		if executeErr == nil {
			if result == nil {
				return nil, ErrUnifiedGatewayRuntimeUnsupported
			}
			if result.Pending && strings.TrimSpace(result.UpstreamRequestID) != "" {
				s.rememberPending(result.UpstreamRequestID, request)
			}
			return unifiedGatewayRuntimeResponseFromExecution(result), nil
		}
		if errors.Is(executeErr, ErrUnifiedGatewayBalanceInsufficient) || errors.Is(executeErr, ErrUnifiedGatewaySnapshotInProgress) || errors.Is(executeErr, ErrUnifiedGatewaySettlementFailed) {
			return nil, executeErr
		}
		lastErr = executeErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrUnifiedGatewayNoEligibleAccount
}

func unifiedGatewayRuntimeResponseFromExecution(result *UnifiedGatewayExecutionResult) *UnifiedGatewayRuntimeResponse {
	if result == nil {
		return nil
	}
	statusCode := http.StatusOK
	if result.Pending {
		statusCode = http.StatusAccepted
	}
	contentType := "application/json"
	if bodyHasSSEFraming(result.ResponseBody) {
		contentType = "text/event-stream"
	}
	return &UnifiedGatewayRuntimeResponse{
		StatusCode:        statusCode,
		ContentType:       contentType,
		Body:              append([]byte(nil), result.ResponseBody...),
		Pending:           result.Pending,
		Charge:            result.Charge,
		MeasuredUnits:     result.MeasuredUnits,
		UpstreamRequestID: result.UpstreamRequestID,
		Record:            result.Record,
	}
}

func (s *UnifiedGatewayRuntimeService) rememberPending(upstreamRequestID string, request UnifiedGatewayRequest) {
	if s == nil || strings.TrimSpace(upstreamRequestID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[unifiedGatewayPendingKey(request.APIKeyID, request.UserID, request.AccessGroupID, upstreamRequestID)] = unifiedGatewayPendingRequest{Request: request}
}

func unifiedGatewayPendingKey(apiKeyID, userID, accessGroupID int64, upstreamRequestID string) string {
	return fmt.Sprintf("%d:%d:%d:%s", apiKeyID, userID, accessGroupID, strings.TrimSpace(upstreamRequestID))
}

func (s *UnifiedGatewayRuntimeService) findPending(ctx context.Context, apiKeyID, userID, accessGroupID int64, upstreamRequestID string) (*UnifiedGatewayPriceSnapshotRecord, UnifiedGatewayRequest, error) {
	if lookup, ok := s.snapshots.(UnifiedGatewayAsyncSnapshotLookup); ok {
		record, err := lookup.FindByUpstreamRequestID(ctx, apiKeyID, userID, accessGroupID, upstreamRequestID)
		if err == nil && record != nil {
			return record, unifiedGatewayRequestFromRecord(record), nil
		}
		if err != nil && !errors.Is(err, ErrUnifiedGatewaySnapshotNotFound) {
			return nil, UnifiedGatewayRequest{}, err
		}
	}
	s.mu.RLock()
	pending, ok := s.pending[unifiedGatewayPendingKey(apiKeyID, userID, accessGroupID, upstreamRequestID)]
	s.mu.RUnlock()
	if !ok {
		return nil, UnifiedGatewayRequest{}, ErrUnifiedGatewayRuntimeRequestNotFound
	}
	record, err := s.snapshots.Get(ctx, pending.Request.APIKeyID, pending.Request.UserID, pending.Request.AccessGroupID, pending.Request.RequestID, pending.Request.AttemptID)
	if err != nil {
		return nil, UnifiedGatewayRequest{}, err
	}
	return record, pending.Request, nil
}

func unifiedGatewayRequestFromRecord(record *UnifiedGatewayPriceSnapshotRecord) UnifiedGatewayRequest {
	if record == nil {
		return UnifiedGatewayRequest{}
	}
	selection := record.Selection
	return UnifiedGatewayRequest{
		RequestID:         record.RequestID,
		AttemptID:         record.AttemptID,
		AccessGroupID:     record.AccessGroupID,
		APIKeyID:          record.APIKeyID,
		UserID:            record.UserID,
		PublicModel:       record.Snapshot.PublicModel,
		Endpoint:          record.Snapshot.Endpoint,
		EstimatedUnits:    record.MeasuredUnits,
		SelectionOverride: &selection,
	}
}

func (s *UnifiedGatewayRuntimeService) Poll(ctx context.Context, apiKeyID, userID, accessGroupID int64, upstreamRequestID string) (*UnifiedGatewayRuntimeResponse, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if !s.accessGroupAllowed(accessGroupID) {
		return nil, ErrUnifiedGatewayUnauthorized
	}
	record, request, err := s.findPending(ctx, apiKeyID, userID, accessGroupID, upstreamRequestID)
	if err != nil {
		return nil, err
	}
	if record.Status == UnifiedGatewaySnapshotCaptured {
		return unifiedGatewayRuntimeResponseFromExecution(unifiedGatewayExecutionFromRecord(record)), nil
	}
	if record.Status == UnifiedGatewaySnapshotReleased {
		return nil, ErrUnifiedGatewayUpstreamFailed
	}
	if record.Status == UnifiedGatewaySnapshotSettlementFailed {
		return nil, ErrUnifiedGatewaySettlementFailed
	}
	poller, ok := s.upstream.(UnifiedGatewayAsyncUpstream)
	if !ok {
		return nil, ErrUnifiedGatewayRuntimeUnsupported
	}
	result, err := poller.Poll(ctx, record.Selection, request, strings.TrimSpace(upstreamRequestID))
	if err != nil {
		// A transient polling transport error must not release the hold or mark
		// the job billable. The next poll can safely retry.
		return nil, err
	}
	completed, err := s.gateway.CompleteAsync(ctx, request, result)
	if err != nil {
		return nil, err
	}
	if completed != nil && completed.Pending {
		s.rememberPending(upstreamRequestID, request)
		s.rememberPending(completed.UpstreamRequestID, request)
	}
	return unifiedGatewayRuntimeResponseFromExecution(completed), nil
}

func (s *UnifiedGatewayRuntimeService) Content(ctx context.Context, apiKeyID, userID, accessGroupID int64, upstreamRequestID string) (*UnifiedGatewayContentResult, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if !s.accessGroupAllowed(accessGroupID) {
		return nil, ErrUnifiedGatewayUnauthorized
	}
	record, request, err := s.findPending(ctx, apiKeyID, userID, accessGroupID, upstreamRequestID)
	if err != nil {
		return nil, err
	}
	if record.Status == UnifiedGatewaySnapshotReleased {
		return nil, ErrUnifiedGatewayUpstreamFailed
	}
	if record.Status == UnifiedGatewaySnapshotSettlementFailed {
		return nil, ErrUnifiedGatewaySettlementFailed
	}
	if record.Status != UnifiedGatewaySnapshotCaptured {
		return nil, ErrUnifiedGatewaySnapshotInProgress
	}
	content, ok := s.upstream.(UnifiedGatewayContentUpstream)
	if !ok {
		return nil, ErrUnifiedGatewayRuntimeUnsupported
	}
	result, err := content.Content(ctx, record.Selection, request, strings.TrimSpace(upstreamRequestID))
	if err != nil {
		return nil, err
	}
	if result.StatusCode == 0 {
		result.StatusCode = http.StatusOK
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: content status %d", ErrUnifiedGatewayUpstreamFailed, result.StatusCode)
	}
	return &result, nil
}

func (s *UnifiedGatewayRuntimeService) ListModels(ctx context.Context, accessGroupID int64) ([]UnifiedGatewayModel, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if !s.accessGroupAllowed(accessGroupID) {
		return nil, ErrUnifiedGatewayUnauthorized
	}
	return s.gateway.ListModels(ctx, accessGroupID)
}
