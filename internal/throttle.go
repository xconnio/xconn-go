package internal

import (
	"context"
	"time"

	"github.com/projectdiscovery/ratelimit"
)

type Strategy int

const (
	Burst Strategy = iota // Process all requests instantly up to the limit
	LeakyBucket
)

type Throttle struct {
	rate     uint          // max messages in duration
	duration time.Duration // duration for the rate
	strategy Strategy      // strategy for handling throttle
}

func NewThrottle(rate uint, duration time.Duration, strategy Strategy) *Throttle {
	return &Throttle{
		rate:     rate,
		duration: duration,
		strategy: strategy,
	}
}

func (t *Throttle) Create() *ratelimit.Limiter {
	if t.strategy == LeakyBucket {
		return ratelimit.NewLeakyBucket(context.Background(), t.rate, t.duration)
	}
	return ratelimit.New(context.Background(), t.rate, t.duration)
}
