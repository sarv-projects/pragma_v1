package daemon

import (
	"context"
	"log"
	"time"
)

type HealthMonitor struct {
	client     *Client
	lifecycle  *Lifecycle
	interval   time.Duration
	maxFails   int
	fails      int
	restarts   int
	maxRestart int
	onFatal    func() // called when restarts are exhausted
}

func NewHealthMonitor(client *Client, lifecycle *Lifecycle, onFatal func()) *HealthMonitor {
	return &HealthMonitor{
		client:     client,
		lifecycle:  lifecycle,
		interval:   5 * time.Second,
		maxFails:   3,
		maxRestart: 3,
		onFatal:    onFatal,
	}
}

func (h *HealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Using a short timeout for the ping
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err := h.client.Call(pingCtx, "ping", nil)
			cancel()

			if err != nil {
				h.fails++
				log.Printf("Daemon health check failed (%d/%d): %v", h.fails, h.maxFails, err)

				if h.fails >= h.maxFails {
					h.restarts++
					if h.restarts > h.maxRestart {
						log.Printf("Daemon max restarts exceeded (%d), signalling fatal", h.maxRestart)
						if h.onFatal != nil {
							h.onFatal()
						}
						return
					}

					log.Printf("Restarting daemon...")
					_ = h.lifecycle.Restart(ctx)
					_ = h.client.Reconnect(h.lifecycle.SocketPath())
					h.fails = 0
				}
			} else {
				// Reset fail counter on success
				h.fails = 0
			}
		}
	}
}
