package service

import "context"

type openAIBypassSchedulerSnapshotContextKey struct{}

func withOpenAIBypassSchedulerSnapshot(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIBypassSchedulerSnapshotContextKey{}, true)
}

func openAIBypassSchedulerSnapshot(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(openAIBypassSchedulerSnapshotContextKey{}).(bool)
	return value
}
