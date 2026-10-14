package audit

import "context"

// RequestContext 承载一次请求的审计上下文。
//
// 领域层不得依赖 Gin，因此传输层负责提取这些值并通过上下文传入，
// 用例层只从上下文读取（与 identity.WithPrincipal 同构）。
type RequestContext struct {
	// RequestID 把审计记录与一次请求关联起来。
	RequestID string
	// IPAddress 是来源地址，仅作辅助线索。
	IPAddress string
	// UserAgent 是客户端标识。
	UserAgent string
}

// requestContextKey 是审计上下文在 context 中的键，使用私有类型避免冲突。
type requestContextKey struct{}

// WithRequestContext 把审计上下文写入 context。
func WithRequestContext(ctx context.Context, rc RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// RequestContextFrom 读取审计上下文，未设置时返回零值。
//
// 缺失审计上下文不得让业务失败：留痕是必要项，但请求上下文只是增强信息。
func RequestContextFrom(ctx context.Context) (RequestContext, bool) {
	rc, ok := ctx.Value(requestContextKey{}).(RequestContext)
	return rc, ok
}
