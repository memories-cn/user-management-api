package bootstrap

import (
	"github.com/example/iam-service/internal/adapters/ratelimit"
)

// RateLimiter 是 bootstrap 包对内存限流器的别名，
// 使传输层可以引用具体类型而不必直接依赖 adapters 包。
type RateLimiter = ratelimit.MemoryLimiter
