package ratelimit

import "errors"

var (
	// errNonPositiveRPS 表示每秒请求数不是正数。
	errNonPositiveRPS = errors.New("限流速率必须大于 0")
	// errNonPositiveBurst 表示令牌桶容量小于 1。
	errNonPositiveBurst = errors.New("令牌桶容量必须不小于 1")
)
