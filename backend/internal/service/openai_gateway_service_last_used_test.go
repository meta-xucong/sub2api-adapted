package service

import "testing"

func TestOpenAIGatewayServiceScheduleAccountLastUsed(t *testing.T) {
	deferred := &DeferredService{}
	svc := &OpenAIGatewayService{deferredService: deferred}

	svc.ScheduleAccountLastUsed(42)

	if _, ok := deferred.lastUsedUpdates.Load(int64(42)); !ok {
		t.Fatal("expected accepted media request to schedule account last-used update")
	}
}

func TestOpenAIGatewayServiceScheduleAccountLastUsedIgnoresInvalidInput(t *testing.T) {
	deferred := &DeferredService{}
	svc := &OpenAIGatewayService{deferredService: deferred}

	svc.ScheduleAccountLastUsed(0)

	called := false
	deferred.lastUsedUpdates.Range(func(_, _ any) bool {
		called = true
		return false
	})
	if called {
		t.Fatal("invalid account id must not schedule an update")
	}
}
