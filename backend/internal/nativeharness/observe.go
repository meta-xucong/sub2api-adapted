package nativeharness

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// InvokeStandard permits admission only on the first call. Further calls can
// only observe/recover that frozen operation, never create absent Bridge work.
// A timeout or client cancellation stops observation, not the native task.
func (s *Service) InvokeStandard(ctx context.Context, userID, keyID int64, clientKey string, canExecute bool, raw []byte) (*Response, error) {
	if ctx.Err() != nil {
		return nil, fail("NATIVE_OBSERVATION_TIMEOUT", 504)
	}
	seconds := s.Config.StandardWaitSeconds
	if seconds < 0 || seconds > 300 {
		return nil, fail("NATIVE_WAIT_CONFIG_INVALID", 503)
	}
	if seconds == 0 {
		return s.Invoke(ctx, userID, keyID, clientKey, canExecute, raw)
	}
	observedCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	result, err := s.Invoke(observedCtx, userID, keyID, clientKey, canExecute, raw)
	for err != nil {
		if observedCtx.Err() != nil {
			return nil, fail("NATIVE_OBSERVATION_TIMEOUT", 504)
		}
		var e *Error
		// Only an explicit waiting-for-the-same-task result is eligible. Unknown
		// transport receipts, approvals, terminal failures and errors do not loop.
		if !errors.As(err, &e) || e.Code != "TASK_WAIT_TIMEOUT" || e.Status != http.StatusGatewayTimeout {
			return nil, err
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-observedCtx.Done():
			timer.Stop()
			return nil, fail("NATIVE_OBSERVATION_TIMEOUT", 504)
		case <-timer.C:
		}
		if observedCtx.Err() != nil {
			return nil, fail("NATIVE_OBSERVATION_TIMEOUT", 504)
		}
		result, err = s.Invoke(observedCtx, userID, keyID, clientKey, false, raw)
	}
	return result, nil
}
