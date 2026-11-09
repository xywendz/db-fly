// Package provider 定义业务模块的注册中心。
// 注册表是通用层与各数据库垂直模块之间唯一的连接点。
package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dbclient/dbclient/internal/domain"
)

var (
	mu        sync.RWMutex
	providers = make(map[domain.Kind]domain.Provider)
)

// Register 在 init 阶段注册一个业务模块；重复注册直接 panic，便于及早发现装配错误。
func Register(p domain.Provider) {
	if p == nil || !p.Kind().Valid() {
		panic("provider: 非法注册")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := providers[p.Kind()]; exists {
		panic(fmt.Sprintf("provider: %s 重复注册", p.Kind()))
	}
	providers[p.Kind()] = p
}

// Get 返回指定类型的业务模块。
func Get(k domain.Kind) (domain.Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[k]
	if !ok {
		return nil, domain.NewError(domain.CodeProviderMissing, "provider.Get",
			"未注册的数据库业务模块: %s", k)
	}
	return p, nil
}

// Kinds 返回已注册的类型，按名称排序保证 UI 顺序稳定。
func Kinds() []domain.Kind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]domain.Kind, 0, len(providers))
	for k := range providers {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Capabilities 查询某个类型的能力集合，无需建立连接。
func Capabilities(k domain.Kind) (domain.CapabilitySet, error) {
	p, err := Get(k)
	if err != nil {
		return nil, err
	}
	return p.Capabilities(), nil
}

// Open 通过注册中心建立会话，通用层不感知具体驱动。
func Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	p, err := Get(cfg.Kind)
	if err != nil {
		return nil, err
	}
	return p.Open(ctx, cfg)
}
