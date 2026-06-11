//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

var _ OpsRepository = (*stubOpsRepo)(nil)

type stubOpsRepo struct {
	OpsRepository
	overview       *OpsDashboardOverview
	err            error
	alertRules     []*OpsAlertRule
	activeEvent    *OpsAlertEvent
	updatedEventID int64
	updatedStatus  string
	resolvedAt     *time.Time
}

func (s *stubOpsRepo) GetDashboardOverview(ctx context.Context, filter *OpsDashboardFilter) (*OpsDashboardOverview, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.overview != nil {
		return s.overview, nil
	}
	return &OpsDashboardOverview{}, nil
}

func (s *stubOpsRepo) GetLatestSystemMetrics(ctx context.Context, windowMinutes int) (*OpsSystemMetricsSnapshot, error) {
	return &OpsSystemMetricsSnapshot{}, nil
}

func (s *stubOpsRepo) ListAlertRules(ctx context.Context) ([]*OpsAlertRule, error) {
	return s.alertRules, nil
}

func (s *stubOpsRepo) GetActiveAlertEvent(ctx context.Context, ruleID int64) (*OpsAlertEvent, error) {
	return s.activeEvent, nil
}

func (s *stubOpsRepo) UpdateAlertEventStatus(ctx context.Context, eventID int64, status string, resolvedAt *time.Time) error {
	s.updatedEventID = eventID
	s.updatedStatus = status
	s.resolvedAt = resolvedAt
	return nil
}

func (s *stubOpsRepo) UpsertJobHeartbeat(ctx context.Context, input *OpsUpsertJobHeartbeatInput) error {
	return nil
}

func TestComputeGroupAvailableRatio(t *testing.T) {
	t.Parallel()

	t.Run("正常情况: 10个账号, 8个可用 = 80%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  10,
			AvailableCount: 8,
		})
		require.InDelta(t, 80.0, got, 0.0001)
	})

	t.Run("边界情况: TotalAccounts = 0 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  0,
			AvailableCount: 8,
		})
		require.Equal(t, 0.0, got)
	})

	t.Run("边界情况: AvailableCount = 0 应返回 0%", func(t *testing.T) {
		t.Parallel()

		got := computeGroupAvailableRatio(&GroupAvailability{
			TotalAccounts:  10,
			AvailableCount: 0,
		})
		require.Equal(t, 0.0, got)
	})
}

func TestCountAccountsByCondition(t *testing.T) {
	t.Parallel()

	t.Run("测试限流账号统计: acc.IsRateLimited", func(t *testing.T) {
		t.Parallel()

		accounts := map[int64]*AccountAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: false},
			3: {IsRateLimited: true},
		}

		got := countAccountsByCondition(accounts, func(acc *AccountAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(2), got)
	})

	t.Run("测试错误账号统计（排除临时不可调度）: acc.HasError && acc.TempUnschedulableUntil == nil", func(t *testing.T) {
		t.Parallel()

		until := time.Now().UTC().Add(5 * time.Minute)
		accounts := map[int64]*AccountAvailability{
			1: {HasError: true},
			2: {HasError: true, TempUnschedulableUntil: &until},
			3: {HasError: false},
		}

		got := countAccountsByCondition(accounts, func(acc *AccountAvailability) bool {
			return acc.HasError && acc.TempUnschedulableUntil == nil
		})
		require.Equal(t, int64(1), got)
	})

	t.Run("边界情况: 空 map 应返回 0", func(t *testing.T) {
		t.Parallel()

		got := countAccountsByCondition(map[int64]*AccountAvailability{}, func(acc *AccountAvailability) bool {
			return acc.IsRateLimited
		})
		require.Equal(t, int64(0), got)
	})
}

// TestComputeRuleMetric_AccountTempUnscheduledCount verifies the new
// account_temp_unscheduled_count metric counts accounts currently in the
// temp-unscheduled window and ignores those whose window has expired or
// were never temp-unscheduled.
func TestComputeRuleMetric_AccountTempUnscheduledCount(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	futureUntil := now.Add(5 * time.Minute)
	pastUntil := now.Add(-1 * time.Minute)

	availability := &OpsAccountAvailability{
		Accounts: map[int64]*AccountAvailability{
			// currently temp-unscheduled (window active)
			1: {TempUnschedulableUntil: &futureUntil},
			2: {TempUnschedulableUntil: &futureUntil},
			// temp-unsched window already expired → should NOT count
			3: {TempUnschedulableUntil: &pastUntil},
			// never temp-unscheduled
			4: {HasError: true},
			5: {IsRateLimited: true},
		},
	}

	opsService := &OpsService{
		getAccountAvailability: func(_ context.Context, _ string, _ *int64) (*OpsAccountAvailability, error) {
			return availability, nil
		},
	}
	svc := &OpsAlertEvaluatorService{
		opsService: opsService,
		opsRepo:    &stubOpsRepo{},
	}

	rule := &OpsAlertRule{MetricType: "account_temp_unscheduled_count"}
	val, ok := svc.computeRuleMetric(context.Background(), rule, nil,
		now.Add(-5*time.Minute), now, "", nil)

	require.True(t, ok)
	require.InDelta(t, 2.0, val, 0.0001, "only 2 accounts have an active temp-unsched window")
}

func TestComputeRuleMetricNewIndicators(t *testing.T) {
	t.Parallel()

	groupID := int64(101)
	platform := "openai"

	availability := &OpsAccountAvailability{
		Group: &GroupAvailability{
			GroupID:        groupID,
			TotalAccounts:  10,
			AvailableCount: 8,
		},
		Accounts: map[int64]*AccountAvailability{
			1: {IsRateLimited: true},
			2: {IsRateLimited: true},
			3: {HasError: true},
			4: {HasError: true, TempUnschedulableUntil: timePtr(time.Now().UTC().Add(2 * time.Minute))},
			5: {HasError: false, IsRateLimited: false},
		},
	}

	opsService := &OpsService{
		getAccountAvailability: func(_ context.Context, _ string, _ *int64) (*OpsAccountAvailability, error) {
			return availability, nil
		},
	}

	svc := &OpsAlertEvaluatorService{
		opsService: opsService,
		opsRepo:    &stubOpsRepo{overview: &OpsDashboardOverview{}},
	}

	start := time.Now().UTC().Add(-5 * time.Minute)
	end := time.Now().UTC()
	ctx := context.Background()

	tests := []struct {
		name       string
		metricType string
		groupID    *int64
		wantValue  float64
		wantOK     bool
	}{
		{
			name:       "group_available_accounts",
			metricType: "group_available_accounts",
			groupID:    &groupID,
			wantValue:  8,
			wantOK:     true,
		},
		{
			name:       "group_available_ratio",
			metricType: "group_available_ratio",
			groupID:    &groupID,
			wantValue:  80.0,
			wantOK:     true,
		},
		{
			name:       "account_rate_limited_count",
			metricType: "account_rate_limited_count",
			groupID:    nil,
			wantValue:  2,
			wantOK:     true,
		},
		{
			name:       "account_error_count",
			metricType: "account_error_count",
			groupID:    nil,
			wantValue:  1,
			wantOK:     true,
		},
		{
			name:       "group_available_accounts without group_id returns false",
			metricType: "group_available_accounts",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
		{
			name:       "group_available_ratio without group_id returns false",
			metricType: "group_available_ratio",
			groupID:    nil,
			wantValue:  0,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := &OpsAlertRule{
				MetricType: tt.metricType,
			}
			gotValue, gotOK := svc.computeRuleMetric(ctx, rule, nil, start, end, platform, tt.groupID)
			require.Equal(t, tt.wantOK, gotOK)
			if !tt.wantOK {
				return
			}
			require.InDelta(t, tt.wantValue, gotValue, 0.0001)
		})
	}
}

func TestComputeRuleMetricRequestThresholdFilters(t *testing.T) {
	t.Parallel()

	start := time.Now().UTC().Add(-5 * time.Minute)
	end := time.Now().UTC()
	ctx := context.Background()

	t.Run("skip error rate when request volume below min_request_count", func(t *testing.T) {
		t.Parallel()

		svc := &OpsAlertEvaluatorService{
			opsRepo: &stubOpsRepo{
				overview: &OpsDashboardOverview{
					RequestCountSLA: 11,
					ErrorCountSLA:   1,
					ErrorRate:       1.0 / 11.0,
				},
			},
		}

		rule := &OpsAlertRule{
			MetricType: "error_rate",
			Filters: map[string]any{
				"min_request_count": 20,
			},
		}

		value, ok := svc.computeRuleMetric(ctx, rule, nil, start, end, "", nil)
		require.False(t, ok)
		require.Equal(t, 0.0, value)
	})

	t.Run("skip success rate when error count below min_error_count", func(t *testing.T) {
		t.Parallel()

		svc := &OpsAlertEvaluatorService{
			opsRepo: &stubOpsRepo{
				overview: &OpsDashboardOverview{
					RequestCountSLA: 11,
					ErrorCountSLA:   1,
					SLA:             10.0 / 11.0,
				},
			},
		}

		rule := &OpsAlertRule{
			MetricType: "success_rate",
			Filters: map[string]any{
				"min_error_count": 2,
			},
		}

		value, ok := svc.computeRuleMetric(ctx, rule, nil, start, end, "", nil)
		require.False(t, ok)
		require.Equal(t, 0.0, value)
	})

	t.Run("allow request metric when thresholds are met", func(t *testing.T) {
		t.Parallel()

		svc := &OpsAlertEvaluatorService{
			opsRepo: &stubOpsRepo{
				overview: &OpsDashboardOverview{
					RequestCountSLA: 20,
					ErrorCountSLA:   3,
					ErrorRate:       0.15,
				},
			},
		}

		rule := &OpsAlertRule{
			MetricType: "error_rate",
			Filters: map[string]any{
				"min_request_count": "10",
				"min_error_count":   2.0,
			},
		}

		value, ok := svc.computeRuleMetric(ctx, rule, nil, start, end, "", nil)
		require.True(t, ok)
		require.InDelta(t, 15.0, value, 0.0001)
	})
}

func TestEvaluateOnceResolvesActiveEventWhenRequestThresholdSkipsMetric(t *testing.T) {
	repo := &stubOpsRepo{
		overview: &OpsDashboardOverview{
			RequestCountSLA: 10,
			ErrorCountSLA:   0,
			SLA:             1,
		},
		alertRules: []*OpsAlertRule{
			{
				ID:               2,
				Enabled:          true,
				Name:             "成功率过低",
				Severity:         "P0",
				MetricType:       "success_rate",
				Operator:         "<",
				Threshold:        95,
				WindowMinutes:    5,
				SustainedMinutes: 5,
				Filters: map[string]any{
					"min_error_count": 2,
				},
			},
		},
		activeEvent: &OpsAlertEvent{
			ID:     283,
			RuleID: 2,
			Status: OpsAlertStatusFiring,
		},
	}
	svc := NewOpsAlertEvaluatorService(nil, repo, nil, nil, &config.Config{Ops: config.OpsConfig{Enabled: true}})

	svc.evaluateOnce(time.Minute)

	require.Equal(t, int64(283), repo.updatedEventID)
	require.Equal(t, OpsAlertStatusResolved, repo.updatedStatus)
	require.NotNil(t, repo.resolvedAt)
}
