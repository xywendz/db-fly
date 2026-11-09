// Package redis 是 Redis 键值库垂直业务模块（go-redis，纯 Go）。
// 树形层级映射：连接 → 逻辑 DB（db0..dbN）→ 键；表数据浏览展示键的类型、TTL 与值。
package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindRedis }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

// dbIndex 把 "db3" / "3" / "" 解析成 Redis 逻辑库下标。
func dbIndex(database string) (int, error) {
	name := strings.TrimSpace(database)
	if name == "" {
		return 0, nil
	}
	if strings.HasPrefix(strings.ToLower(name), "db") {
		name = name[2:]
	}
	index, err := strconv.Atoi(name)
	if err != nil || index < 0 || index > 1023 {
		return 0, domain.NewError(domain.CodeValidation, "redis.database",
			"非法的 Redis 逻辑库 %q（可用 db0 / 0）", database)
	}
	return index, nil
}

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	index, err := dbIndex(cfg.Database)
	if err != nil {
		return nil, err
	}
	opts := &goredis.Options{
		Addr:         net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Username:     cfg.Params["username"],
		Password:     cfg.Password,
		DB:           index,
		DialTimeout:  cfg.EffectiveTimeout(),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	switch strings.ToLower(cfg.TLSMode) {
	case "", "false", "disabled", "preferred":
		// 默认明文
	case "true":
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	case "skip-verify":
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	default:
		return nil, domain.NewError(domain.CodeValidation, "redis.options",
			"不支持的 TLS 模式 %q", cfg.TLSMode)
	}
	for k, v := range cfg.Params {
		switch strings.ToLower(k) {
		case "clientname":
			opts.ClientName = v
		case "maxretries":
			if n, convErr := strconv.Atoi(v); convErr == nil {
				opts.MaxRetries = n
			}
		}
	}
	client := goredis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, mapOpenError(err)
	}
	return &session{
		cfg: cfg, base: client, opts: opts, caps: capabilitySet(),
		clients: map[int]*goredis.Client{index: client},
	}, nil
}

// session 为每个逻辑库维护一个独立客户端（Redis 的 SELECT 是连接级状态）。
type session struct {
	cfg     domain.ConnectionConfig
	base    *goredis.Client
	opts    *goredis.Options
	caps    domain.CapabilitySet
	mu      sync.Mutex
	clients map[int]*goredis.Client
}

func (s *session) Kind() domain.Kind                    { return domain.KindRedis }
func (s *session) Capabilities() domain.CapabilitySet   { return s.caps.Clone() }
func (s *session) Metadata() domain.MetadataBrowser     { return &metadataBrowser{session: s} }
func (s *session) Query() domain.QueryExecutor          { return &queryExecutor{session: s} }
func (s *session) Schema() domain.SchemaEditor          { return &schemaEditor{session: s} }
func (s *session) Data() domain.DataEditor              { return &dataEditor{} }
func (s *session) Maintenance() domain.TableMaintenance { return nil }

func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for index, client := range s.clients {
		if err := client.Close(); err != nil && first == nil {
			first = err
		}
		delete(s.clients, index)
	}
	return first
}

// clientFor 取得指定逻辑库的客户端；database 为空时返回连接配置默认库。
func (s *session) clientFor(database string) (*goredis.Client, error) {
	index := s.opts.DB
	if strings.TrimSpace(database) != "" {
		parsed, err := dbIndex(database)
		if err != nil {
			return nil, err
		}
		index = parsed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if client, ok := s.clients[index]; ok {
		return client, nil
	}
	options := *s.opts
	options.DB = index
	client := goredis.NewClient(&options)
	s.clients[index] = client
	return client, nil
}

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	info, err := s.base.Info(ctx, "server").Result()
	if err != nil {
		return domain.ServerInfo{}, mapQueryError(err)
	}
	version := "unknown"
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "redis_version:") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
			break
		}
	}
	return domain.ServerInfo{
		Kind: domain.KindRedis, Version: version, Edition: "Redis", Host: s.cfg.Host,
	}, nil
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: false, // 逻辑库数量由服务端配置决定
		domain.CapAlterDatabase:  false,
		domain.CapCreateTable:    true, // 语义为创建一个键
		domain.CapAlterTable:     false,
		domain.CapDropObject:     true, // 语义为 DEL 键
		domain.CapSchemas:        false,
		domain.CapSQL:            false,
		domain.CapMongoFilter:    false,
		domain.CapJSONTree:       false,
		domain.CapExplain:        false,
		domain.CapTransactions:   false,
		domain.CapDataEdit:       false,
		domain.CapKeyValues:      true,
		domain.CapShowCreate:     true,
	}
}

func mapOpenError(err error) error {
	return domain.WrapError(domain.CodeConnection, "redis.Open", err,
		"无法连接 Redis 服务：%s", err.Error())
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if err == goredis.Nil {
		return domain.NewError(domain.CodeNotFound, "redis.query", "键不存在")
	}
	if domain.CodeOf(err) == domain.CodeCanceled {
		return domain.WrapError(domain.CodeCanceled, "redis.query", err, "操作已取消")
	}
	if domain.CodeOf(err) == domain.CodeTimeout {
		return domain.WrapError(domain.CodeTimeout, "redis.query", err, "操作超时")
	}
	return domain.WrapError(domain.CodeQuery, "redis.query", err,
		"Redis 命令执行失败：%s", err.Error())
}

// splitCommand 按 shell 风格拆分命令，支持单/双引号与反斜杠转义。
func splitCommand(text string) ([]any, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
		started bool
		escape  bool
	)
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range text {
		switch {
		case escape:
			current.WriteRune(r)
			started = true
			escape = false
		case r == '\\' && quote != 0:
			escape = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, domain.NewError(domain.CodeValidation, "redis.command", "命令中的引号未闭合")
	}
	flush()
	if len(args) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "redis.command", "命令不能为空")
	}
	out := make([]any, len(args))
	for i := range args {
		out[i] = args[i]
	}
	return out, nil
}

// formatValue 把 Redis 返回的任意值渲染成可展示文本。
func formatValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "(nil)"
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, formatValue(item))
		}
		return strings.Join(parts, "\n")
	case map[any]any:
		parts := make([]string, 0, len(v))
		for k, item := range v {
			parts = append(parts, fmt.Sprintf("%v: %s", k, formatValue(item)))
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		parts := make([]string, 0, len(v))
		for k, item := range v {
			parts = append(parts, fmt.Sprintf("%s: %s", k, formatValue(item)))
		}
		return strings.Join(parts, "\n")
	default:
		return sqlkit.Stringify(v)
	}
}

var redisWriteCommands = map[string]bool{
	"SET": true, "SETNX": true, "SETEX": true, "PSETEX": true, "MSET": true,
	"DEL": true, "UNLINK": true, "EXPIRE": true, "PEXPIRE": true, "PERSIST": true,
	"APPEND": true, "INCR": true, "DECR": true, "INCRBY": true, "DECRBY": true,
	"HSET": true, "HMSET": true, "HDEL": true, "HINCRBY": true,
	"LPUSH": true, "RPUSH": true, "LPOP": true, "RPOP": true, "LSET": true, "LREM": true,
	"SADD": true, "SREM": true, "ZADD": true, "ZREM": true,
	"XADD": true, "XDEL": true, "FLUSHDB": true, "FLUSHALL": true,
	"RENAME": true, "RENAMENX": true, "COPY": true, "RESTORE": true,
}

func isWriteCommand(args []any) bool {
	if len(args) == 0 {
		return false
	}
	name, ok := args[0].(string)
	if !ok {
		return false
	}
	return redisWriteCommands[strings.ToUpper(strings.TrimSpace(name))]
}
