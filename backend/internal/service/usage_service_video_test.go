package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type videoUsageRepoStub struct {
	UsageLogRepository
	logs []UsageLog
}

func (r *videoUsageRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, filter usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	for _, log := range r.logs {
		if log.UserID == filter.UserID && log.RequestID == filter.RequestID {
			return []UsageLog{log}, &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 2, Pages: 1}, nil
		}
	}
	return nil, &pagination.PaginationResult{Total: 0, Page: 1, PageSize: 2, Pages: 1}, nil
}

func TestUsageServiceGetVideoUsageUsesExistingRequestLogAndGrokStableKey(t *testing.T) {
	svc := NewUsageService(&videoUsageRepoStub{logs: []UsageLog{{
		UserID: 42, RequestID: "grok-video:req-1", Model: "grok-imagine-video-1.5", ActualCost: 0.125,
	}}}, nil, nil, nil)
	fact, err := svc.GetVideoUsage(context.Background(), 42, "req-1")
	require.NoError(t, err)
	require.Equal(t, VideoUsageFact{UserID: 42, RequestID: "grok-video:req-1", Model: "grok-imagine-video-1.5", ActualCost: "0.12500000"}, fact)
}

func TestUsageServiceGetVideoUsageFailsClosedForMissingOrInvalidRows(t *testing.T) {
	svc := NewUsageService(&videoUsageRepoStub{logs: []UsageLog{{
		UserID: 42, RequestID: "req-2", Model: "grok-imagine-video-1.5", ActualCost: 0,
	}}}, nil, nil, nil)
	_, err := svc.GetVideoUsage(context.Background(), 42, "req-missing")
	require.ErrorIs(t, err, ErrUsageLogNotFound)
	_, err = svc.GetVideoUsage(context.Background(), 42, "req-2")
	require.Error(t, err)
}
