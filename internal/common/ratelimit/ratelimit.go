package ratelimit

import (
	"context"
	"golang.org/x/time/rate"
)

type Limiter struct {
	limiter *rate.Limiter
}

// New creates a new rate limiter.
// r: limit (events per second)
// b: burst size
func New(r float64, b int) *Limiter {
	return &Limiter{
		limiter: rate.NewLimiter(rate.Limit(r), b),
	}
}

// Wait blocks until the limiter permits an event to happen.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}

// Allow reports whether an event may happen at time now.
func (l *Limiter) Allow() bool {
	return l.limiter.Allow()
}