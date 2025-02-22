package repeat

import (
	"context"
	"sync"
	"time"
)

// Start a goroutine which repeatedly calls run and then sleep for interval between each
// call. The goroutine runs until the context is cancelled.
func Start(ctx context.Context, interval time.Duration, run func(context.Context)) {
	go func() {
		run(ctx)

		for {
			timer := time.NewTimer(interval)
			select {
			case <-timer.C:
				run(ctx)
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
}

// InGroup starts a repeated goroutine as part of a group
func InGroup(wg *sync.WaitGroup, ctx context.Context, cancel context.CancelFunc, interval time.Duration, run func(context.Context, context.CancelFunc)) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		run(ctx, cancel)

		for {
			timer := time.NewTimer(interval)
			select {
			case <-timer.C:
				run(ctx, cancel)
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
}
