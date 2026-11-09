package sqlkit

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
)

// PoolSet 按数据库名懒建立独立连接池。
// PostgreSQL 等不允许跨库限定名，浏览其它库时必须切换到指向该库的连接池。
type PoolSet struct {
	mu          sync.Mutex
	driver      string
	dsnFor      func(database string) (string, error)
	pools       map[string]*sql.DB
	defaultName string
}

// NewPoolSet 用已建立并 Ping 成功的初始连接池装配 PoolSet。
func NewPoolSet(driver, defaultName string, initial *sql.DB, dsnFor func(string) (string, error)) *PoolSet {
	p := &PoolSet{
		driver:      driver,
		dsnFor:      dsnFor,
		pools:       make(map[string]*sql.DB),
		defaultName: defaultName,
	}
	key := defaultName
	if key == "" {
		key = "default"
	}
	p.pools[key] = initial
	return p
}

// DB 返回绑定到指定库的连接池；database 为空时返回默认池。
func (p *PoolSet) DB(ctx context.Context, database string) (*sql.DB, error) {
	name := database
	if name == "" {
		name = p.defaultName
	}
	if name == "" || name == p.defaultName {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.pools[poolKey(p.defaultName)], nil
	}
	p.mu.Lock()
	existing := p.pools[name]
	p.mu.Unlock()
	if existing != nil {
		return existing, nil
	}
	if p.dsnFor == nil {
		return nil, domain.NewError(domain.CodeUnsupported, "sqlkit.pool",
			"该模块不支持切换到数据库 %q", name)
	}
	dsn, err := p.dsnFor(name)
	if err != nil {
		return nil, err
	}
	pool, err := sql.Open(p.driver, dsn)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "sqlkit.pool", err, "初始化连接池失败")
	}
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(2)
	pool.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, domain.WrapError(domain.CodeConnection, "sqlkit.pool", err,
			"无法连接到数据库 %q", name)
	}
	p.mu.Lock()
	if existing = p.pools[name]; existing != nil {
		p.mu.Unlock()
		_ = pool.Close()
		return existing, nil
	}
	p.pools[name] = pool
	p.mu.Unlock()
	return pool, nil
}

func poolKey(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// Close 关闭全部连接池。
func (p *PoolSet) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var first error
	for name, pool := range p.pools {
		if err := pool.Close(); err != nil && first == nil {
			first = err
		}
		delete(p.pools, name)
	}
	return first
}
