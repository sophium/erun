package main

import (
	"context"
)

func (a *App) activityWatcherCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
