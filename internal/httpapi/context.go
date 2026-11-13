package httpapi

import (
	"context"

	"github.com/db-fly/db-fly/internal/auth"
)

// 当前请求的所属用户运行时由 withUser 中间件注入，
// RPC / 上传 / SSE 都从这里取，确保不会串到别的用户的数据。
type scopeKey struct{}

func withRuntime(ctx context.Context, rt *auth.Runtime) context.Context {
	return context.WithValue(ctx, scopeKey{}, rt)
}

func runtimeFrom(ctx context.Context) (*auth.Runtime, bool) {
	rt, ok := ctx.Value(scopeKey{}).(*auth.Runtime)
	return rt, ok
}
