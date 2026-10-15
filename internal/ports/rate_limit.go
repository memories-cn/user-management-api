package ports

import (
	"context"
	"time"
)

// RateLimitDecision 描述一次限流判定的结果。
type RateLimitDecision struct {
	// Allowed 为 true 表示放行。
	Allowed bool
	// RetryAfter 是被拒绝后建议的等待时长；放行时为 0。
	RetryAfter time.Duration
}

// RateLimiter 抽象限流能力，便于把"按什么键限流"与"如何限流"解耦。
//
// 实现必须是并发安全的：该接口在 HTTP 请求路径上被并发调用。
type RateLimiter interface {
	// Take 尝试为 key 消耗一个令牌。
	//
	// 语义与授权判定刻意区分：本接口返回 error 表示**限流器自身故障**，
	// 调用方应放行并记录 ERROR 日志——限流是可用性保护，不是授权判定，
	// 因保护组件故障而拒绝全部请求比放过请求更糟。
	Take(ctx context.Context, key string) (RateLimitDecision, error)
}
