package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesImagePricingEstimate(t *testing.T) {
	for _, tc := range []struct {
		name, body, model, size, quality string
		units                            int
	}{
		{"image model", `{"model":"gpt-image-2.5"}`, "gpt-image-2.5", "", "", 1},
		{"native auto", `{"model":"gpt-6.1-sol","tools":[{"type":"image_generation","model":"gpt-image-2.5","size":"1024x1024","quality":"high","n":2}],"tool_choice":"auto"}`, "gpt-image-2.5", "1024x1024", "high", 2},
		{"native file input", `{"model":"gpt-6.1-sol","tools":[{"type":"image_generation"}],"input":[{"type":"input_image","file_id":"file_test"}]}`, "gpt-image-2", "", "", 1},
		{"text", `{"model":"gpt-6.1-sol","input":"hello"}`, "", "", "", 0},
		{"passive namespace", `{"model":"gpt-6.1-sol","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"text2im"}]}]}`, "", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			estimate := OpenAIResponsesImageInflightEstimate("gpt-6.1-sol", body)
			if tc.model == "" {
				require.Nil(t, estimate)
				return
			}
			require.Equal(t, tc.model, estimate.Model)
			require.Equal(t, InflightEstimateImage, estimate.Kind)
			require.Equal(t, tc.size, estimate.ImageSize)
			require.Equal(t, tc.quality, estimate.ImageQuality)
			require.Equal(t, tc.units, estimate.Units)
			require.Equal(t, tc.body, string(body))
		})
	}
}

func TestResponsesImageRoutePricingKeepsTextAndImageSnapshots(t *testing.T) {
	groupID := int64(16)
	key := &APIKey{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformComposite}}
	imageCard := UnifiedGatewayRoutePricingEntry{AccountID: 27, Model: "gpt-image-2.5", Kind: UnifiedGatewayRoutePricingImage, UnitPrice: routePricingFloat(.069), ImagePricingMode: UnifiedGatewayImagePricingFlatPerImage}
	textCard := UnifiedGatewayRoutePricingEntry{AccountID: 27, Model: "gpt-6.1-sol", Kind: UnifiedGatewayRoutePricingToken, Multiplier: routePricingFloat(.5)}
	for _, tc := range []struct {
		name        string
		cards       []UnifiedGatewayRoutePricingEntry
		image, text float64
	}{
		{"both", []UnifiedGatewayRoutePricingEntry{imageCard, textCard}, .08625, .05},
		{"image only", []UnifiedGatewayRoutePricingEntry{imageCard}, .08625, .1},
		{"text only", []UnifiedGatewayRoutePricingEntry{textCard}, .0875, .05},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := buildUnifiedGatewayRoutePricingSnapshot(UnifiedGatewayRoutePricingConfig{TargetGroupID: groupID, Revision: 1, Entries: tc.cards})
			require.NoError(t, err)
			settings := &SettingService{}
			settings.routePricingSnapshot.Store(snapshot)
			req := InflightEstimateRequest{Model: "gpt-6.1-sol", Kind: InflightEstimateToken, ResponsesImage: OpenAIResponsesImageInflightEstimate("gpt-6.1-sol", []byte(`{"model":"gpt-6.1-sol","tools":[{"type":"image_generation","model":"gpt-image-2.5"}],"tool_choice":"auto"}`))}
			ctx, estimate, planned := settings.prepareUnifiedGatewayRoutePricingDecision(context.Background(), key, req, .1, true, 1.25)
			require.True(t, planned)
			require.GreaterOrEqual(t, estimate, tc.image)
			ctx = WithUnifiedGatewayRoutePricingDecisionAllowed(ctx, true)
			// The async settlement context must carry both immutable price snapshots.
			ctx = CopyUnifiedGatewayRoutePricingDecision(ctx, context.Background())
			for _, result := range []struct {
				model        string
				count        int
				native, want float64
			}{
				{"gpt-image-2.5", 1, .0875, tc.image}, {"gpt-6.1-sol", 0, .1, tc.text},
			} {
				cost := &CostBreakdown{TotalCost: .07, ActualCost: result.native}
				got := ApplyUnifiedGatewayRoutePricing(ctx, key, 27, result.model, "2K", "", nil, result.count, 0, "", 0, cost, 1.25, 1)
				require.InDelta(t, result.want, got.ActualCost, 1e-12)
				require.Equal(t, .07, got.TotalCost)
			}
			for _, account := range []int64{27, 18} {
				blocked := WithUnifiedGatewayRoutePricingDecisionAllowed(ctx, false)
				if account == 18 {
					blocked = ctx
				}
				cost := &CostBreakdown{TotalCost: .07, ActualCost: .0875}
				got := ApplyUnifiedGatewayRoutePricing(blocked, key, account, "gpt-image-2.5", "2K", "", nil, 1, 0, "", 0, cost, 1.25, 1)
				require.Equal(t, .0875, got.ActualCost)
			}
		})
	}
}
