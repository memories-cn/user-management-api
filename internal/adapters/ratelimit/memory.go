// Package ratelimit 提供进程内的令牌桶限流实现。
//
// 定位：这是**可用性保护**，不是授权判定。因此实现自身的故障
// （如内存压力）由调用方决定放行并记录 ERROR 日志，而不是拒绝全部请求。
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/example/iam-service/internal/ports"
)

// 编译期断言：确保实现 ports.RateLimiter。
var _ ports.RateLimiter = (*MemoryLimiter)(nil)

// bucketTTL 是未被使用的限流桶的保留时长。
//
// 必须显著长于限流窗口：清理过早会让闲置 key 在重建后立刻获得满桶令牌，
// 出现"清理后瞬间放行一批"的抖动。
const bucketTTL = 10 * time.Minute

// evictionInterval 是清理过期桶的周期。
const evictionInterval = time.Minute

// MemoryLimiter 基于 golang.org/x/time/rate 的进程内令牌桶限流器。
//
// 多实例部署时每个实例各自限流，累计容量随实例数线性增长——
// 这对滥用防护是可接受的，且避免了为 Redis 引入一套适配层与故障模式。
type MemoryLimiter struct {
	// rps 是每秒允许的请求数。
	rps float64
	// burst 是令牌桶容量。
	burst int

	mu      sync.Mutex
	buckets map[string]*bucket
	// lastSeen 记录每个 key 最后一次使用时间，用于惰性驱逐。
	lastSeen map[string]time.Time
	now      func() time.Time
}

// bucket 是一个限流桶及其最近使用时间。
type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewMemoryLimiter 构造内存限流器。
//
// rps 必须大于 0，burst 必须不小于 1。
func NewMemoryLimiter(rps float64, burst int) (*MemoryLimiter, error) {
	if rps <= 0 {
		return nil, errNonPositiveRPS
	}
	if burst < 1 {
		return nil, errNonPositiveBurst
	}

	return &MemoryLimiter{
		rps:      rps,
		burst:    burst,
		buckets:  make(map[string]*bucket),
		lastSeen: make(map[string]time.Time),
		now:      time.Now,
	}, nil
}

// Take 尝试为 key 消耗一个令牌。
func (l *MemoryLimiter) Take(ctx context.Context, key string) (ports.RateLimitDecision, error) {
	if err := ctx.Err(); err != nil {
		return ports.RateLimitDecision{}, err
	}
	if key == "" {
		// 没有可用的限流键时不限流：宁可放过，也不要把所有无键请求归为一类。
		return ports.RateLimitDecision{Allowed: true}, nil
	}

	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(rate.Limit(l.rps), l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = l.now()
	l.mu.Unlock()

	if !b.limiter.AllowN(l.now(), 1) {
		return ports.RateLimitDecision{
			Allowed:    false,
			RetryAfter: time.Second / time.Duration(l.rps),
		}, nil
	}
	return ports.RateLimitDecision{Allowed: true}, nil
}

// StartEviction 启动过期桶清理，直到 ctx 取消为止。
//
// 没有清理的话，按用户/IP 分桶的 map 会随访问量无限增长，
// 最终变成一次缓慢的内存泄漏。
func (l *MemoryLimiter) StartEviction(ctx context.Context) {
	go l.evictionLoop(ctx)
}

// evictionLoop 周期性清理长时间未使用的桶。
func (l *MemoryLimiter) evictionLoop(ctx context.Context) {
	ticker := time.NewTicker(evictionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.evict(l.now())
		}
	}
}

// evict 删除超过 bucketTTL 未使用的桶。
func (l *MemoryLimiter) evict(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) >= bucketTTL {
			delete(l.buckets, key)
		}
	}
}

// Len 返回当前活跃的限流桶数量，便于测试与排障观察。
func (l *MemoryLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
