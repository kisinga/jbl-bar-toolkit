package main

import (
	"context"
	"log"
	"sync"
	"time"
)

// Model composition owns native monitors and their restoration order. The
// shared HTTP server knows neither these workers nor their device dependencies.
type bar91Lifecycle struct {
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	automationDone chan struct{}
	once           sync.Once
	cleanup        func()
}

func (r *remote) startBar91(parent context.Context) *bar91Lifecycle {
	ctx, cancel := context.WithCancel(parent)
	life := &bar91Lifecycle{cancel: cancel, automationDone: make(chan struct{})}
	start := func(work func(context.Context)) {
		life.workers.Add(1)
		go func() { defer life.workers.Done(); work(ctx) }()
	}
	if !r.cfg.Headless {
		start(monitorNetwork)
		start(monitorCast)
		start(func(ctx context.Context) { monitorDiscovery(ctx, r.network) })
		start(r.extensions.Run)
		life.cleanup = func() {
			// All native background writers have exited. Serialize remaining HTTP
			// work before restoring DSP state and closing private helper bridges.
			r.actionMu.Lock()
			defer r.actionMu.Unlock()
			r.device.mu.Lock()
			defer r.device.mu.Unlock()
			c, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err := r.device.resetFrontEQ(c); err != nil {
				log.Printf("EQ cleanup: %v", err)
			}
			closeEQParser()
			closeBluetoothBridge()
		}
	}
	start(func(ctx context.Context) { defer close(life.automationDone); r.monitorAutoStandard(ctx) })
	return life
}

func (life *bar91Lifecycle) Close() {
	life.once.Do(func() {
		life.cancel()
		life.workers.Wait()
		if life.cleanup != nil {
			life.cleanup()
		}
	})
}
