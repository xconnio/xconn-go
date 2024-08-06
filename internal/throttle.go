package internal

import (
	"context"
	"time"

	"github.com/projectdiscovery/ratelimit"
)

type Throttle struct {
	rate     uint          // max messages in duration
	duration time.Duration // duration for the rate
}

func NewThrottle(rate uint, duration time.Duration) *Throttle {
	return &Throttle{
		rate:     rate,
		duration: duration,
	}
}

func (t *Throttle) Create() *ratelimit.Limiter {
	return ratelimit.New(context.Background(), t.rate, t.duration)
}
