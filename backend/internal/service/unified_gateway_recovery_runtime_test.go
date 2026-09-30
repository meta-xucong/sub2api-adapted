package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type unifiedGatewayRecoveryReadinessFake struct {
	ready bool
}

func (f unifiedGatewayRecoveryReadinessFake) CheckSchema(context.Context) (UnifiedGatewaySchemaReadiness, error) {
	return UnifiedGatewaySchemaReadiness{Ready: f.ready, Missing: []string{"unified_gateway_recovery_tasks"}}, nil
}

func TestUnifiedGatewayRecoveryRuntimeDisabledByGate(t *testing.T) {
	runtime := NewUnifiedGatewayRecoveryRuntime(&UnifiedGatewayReconciler{}, &config.Config{})
	runtime.Start()
	require.False(t, runtime.Running())
	runtime.Stop()
}

func TestUnifiedGatewayRecoveryRuntimeStartStopIsIdempotent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayRuntimeEnabled = true
	runtime := NewUnifiedGatewayRecoveryRuntime(&UnifiedGatewayReconciler{}, cfg)
	runtime.Start()
	require.Eventually(t, runtime.Running, time.Second, time.Millisecond)
	runtime.Start()
	require.True(t, runtime.Running())
	runtime.Stop()
	runtime.Stop()
	require.False(t, runtime.Running())
}

func TestUnifiedGatewayRecoveryRuntimeRequiresMigrationReadiness(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayRuntimeEnabled = true
	runtime := NewUnifiedGatewayRecoveryRuntime(&UnifiedGatewayReconciler{}, cfg, unifiedGatewayRecoveryReadinessFake{})
	runtime.Start()
	require.False(t, runtime.Running())
	runtime.Stop()
}
