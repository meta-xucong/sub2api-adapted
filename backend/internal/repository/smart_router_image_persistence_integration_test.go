//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	core "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterImagePostgresPersistenceAndFreshProcessRestore(t *testing.T) {
	ctx := context.Background()
	require.NotEmpty(t, integrationDSN)
	repo := NewSmartRouterHealthRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Second)
	var events []core.HealthEvent
	for i, capability := range []core.Capability{core.CapabilityImageGeneration, core.CapabilityImageEdit} {
		// Swap model stages between operations so collapsed capability/model
		// keys cannot accidentally satisfy both admission expectations.
		models := []string{"gpt-image-2", "gpt-image-2.5"}
		if i == 1 {
			models[0], models[1] = models[1], models[0]
		}
		for j, stage := range []core.RecoveryStage{core.RecoveryCooling, core.RecoveryModelUnavailable, core.RecoveryNormal} {
			accountID := int64(880_131)
			model := models[0]
			if j == 1 {
				model = models[1]
			}
			if j == 2 {
				accountID++
			}
			event := core.HealthEvent{
				OccurredAtUnix: now.Unix(), Source: "production",
				Key:       core.NewHealthKey(fmt.Sprintf("account:%d/image/pr13-persistence-shared", accountID), capability, model),
				AccountID: accountID, SourceGroup: "pr13-image-persistence",
				RecoveryStage: stage, HealthScore: .5, ErrorRateEWMA: .5,
				ConsecutiveFailures: 2 + i, RecoveryPriority: 32 + j*30,
				StatusCode: 503, FailureClass: core.FailureUpstream5xx,
				Action: "bounded_cooldown", CooldownUntilUnix: now.Add(time.Duration(17+j) * time.Minute).Unix(),
			}
			if stage == core.RecoveryNormal {
				event.Success, event.StatusCode, event.FailureClass = true, 200, ""
				event.HealthScore, event.ErrorRateEWMA = 1, 0
				event.ConsecutiveFailures, event.ConsecutiveSuccesses = 0, 1
				event.RecoveryPriority, event.CooldownUntilUnix = 0, 0
				event.Action = "success"
			}
			require.NoError(t, repo.RecordEvent(ctx, event))
			events = append(events, event)
		}
	}
	fixtureJSON, err := json.Marshal(events)
	require.NoError(t, err)
	backendDir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	goBinary, err := exec.LookPath("go")
	require.NoError(t, err)
	childCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(childCtx, goBinary, "test", "-tags=integration", "-v", "./internal/smartrouter/core", "-run", "^TestSmartRouterImageRestoreThroughPostgresLedgerInChildProcess$", "-count=1")
	cmd.Dir = backendDir
	cmd.Env = append(os.Environ(),
		"SUB2API_SMART_ROUTER_IMAGE_RESTORE_DSN="+integrationDSN,
		"SUB2API_SMART_ROUTER_IMAGE_RESTORE_EVENTS="+string(fixtureJSON),
	)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "fresh-process image restore failed:\n%s", output)
	require.Containsf(t, string(output), "fresh-process image repository state restored with admission boundaries",
		"the image child test must run, not skip:\n%s", output)
	t.Logf("image persistence child process:\n%s", output)
}
