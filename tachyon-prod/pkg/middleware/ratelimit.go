package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.RWMutex
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)
	requests := rl.requests[key]

	var validRequests []time.Time
	for _, t := range requests {
		if t.After(windowStart) {
			validRequests = append(validRequests, t)
		}
	}

	if len(validRequests) >= rl.limit {
		rl.requests[key] = validRequests
		return false
	}

	validRequests = append(validRequests, now)
	rl.requests[key] = validRequests
	return true
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		windowStart := now.Add(-rl.window)

		for key, requests := range rl.requests {
			var validRequests []time.Time
			for _, t := range requests {
				if t.After(windowStart) {
					validRequests = append(validRequests, t)
				}
			}
			if len(validRequests) == 0 {
				delete(rl.requests, key)
			} else {
				rl.requests[key] = validRequests
			}
		}
		rl.mu.Unlock()
	}
}

func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()

		if !limiter.Allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "Rate limit exceeded. Please try again later.",
			})
			return
		}

		c.Next()
	}
}

type EndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	mu       sync.RWMutex
	defaults struct {
		limit  int
		window time.Duration
	}
}

func NewEndpointRateLimiter(defaultLimit int, defaultWindow time.Duration) *EndpointRateLimiter {
	return &EndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
		defaults: struct {
			limit  int
			window time.Duration
		}{
			limit:  defaultLimit,
			window: defaultWindow,
		},
	}
}

func (erl *EndpointRateLimiter) SetLimit(endpoint string, limit int, window time.Duration) {
	erl.mu.Lock()
	defer erl.mu.Unlock()
	erl.limiters[endpoint] = NewRateLimiter(limit, window)
}

func (erl *EndpointRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		endpoint := c.FullPath()
		key := c.ClientIP() + ":" + endpoint

		erl.mu.RLock()
		limiter, exists := erl.limiters[endpoint]
		erl.mu.RUnlock()

		if !exists {
			erl.mu.Lock()
			if _, exists = erl.limiters[endpoint]; !exists {
				erl.limiters[endpoint] = NewRateLimiter(erl.defaults.limit, erl.defaults.window)
			}
			limiter = erl.limiters[endpoint]
			erl.mu.Unlock()
		}

		if !limiter.Allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   "Rate limit exceeded for this endpoint. Please try again later.",
			})
			return
		}

		c.Next()
	}
}

func DefaultRateLimiters() *EndpointRateLimiter {
	erl := NewEndpointRateLimiter(100, time.Minute)

	erl.SetLimit("/settle", 10, time.Minute)
	erl.SetLimit("/store-recipients", 30, time.Minute)
	erl.SetLimit("/delete-recipients/:intentId", 5, time.Minute)
	erl.SetLimit("/start", 5, time.Minute)
	erl.SetLimit("/stop", 5, time.Minute)
	erl.SetLimit("/register-solver", 10, time.Minute)
	erl.SetLimit("/health", 300, time.Minute)
	erl.SetLimit("/get-routes", 200, time.Minute)

	return erl
}
