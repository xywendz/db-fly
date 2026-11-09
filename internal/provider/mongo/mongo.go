// Package mongo 是 MongoDB 垂直业务模块。
// 自包含连接、集合/索引元数据、BSON 查询与命令式 DDL，不依赖其他数据库模块。
package mongo

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/provider"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindMongoDB }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	uri, err := buildURI(cfg)
	if err != nil {
		return nil, err
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "mongo.Open", err, "创建 MongoDB 客户端失败")
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, mapOpenError(err)
	}
	return &session{client: client, cfg: cfg, caps: capabilitySet()}, nil
}

func buildURI(cfg domain.ConnectionConfig) (string, error) {
	u := &url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
	}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	q := url.Values{}
	q.Set("appName", "dbclient")
	q.Set("connectTimeoutMS", strconv.FormatInt(cfg.EffectiveTimeout().Milliseconds(), 10))
	q.Set("serverSelectionTimeoutMS", strconv.FormatInt(cfg.EffectiveTimeout().Milliseconds(), 10))
	if cfg.User != "" {
		if _, ok := cfg.Params["authSource"]; !ok {
			q.Set("authSource", "admin")
		}
	}
	switch cfg.TLSMode {
	case "", "false", "disabled", "preferred":
		// 默认不启用 TLS
	case "true":
		q.Set("tls", "true")
	case "skip-verify":
		q.Set("tls", "true")
		q.Set("tlsInsecure", "true")
	default:
		return "", domain.NewError(domain.CodeValidation, "mongo.uri",
			"不支持的 TLS 模式 %q（可用: false/true/skip-verify）", cfg.TLSMode)
	}
	for k, v := range cfg.Params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// session 持有独立的 mongo.Client 与其连接池。
type session struct {
	client *mongo.Client
	cfg    domain.ConnectionConfig
	caps   domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindMongoDB }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Metadata() domain.MetadataBrowser   { return &metadataBrowser{client: s.client} }
func (s *session) Schema() domain.SchemaEditor        { return &schemaEditor{client: s.client} }
func (s *session) Query() domain.QueryExecutor        { return &queryExecutor{client: s.client} }
func (s *session) Data() domain.DataEditor            { return &dataEditor{client: s.client} }

func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.client.Disconnect(ctx)
}

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	var info struct {
		Version string `bson:"version"`
	}
	result := s.client.Database("admin").RunCommand(ctx, bson.M{"buildInfo": 1})
	if err := result.Decode(&info); err != nil {
		return domain.ServerInfo{}, mapQueryError(err)
	}
	return domain.ServerInfo{
		Kind:    domain.KindMongoDB,
		Version: info.Version,
		Host:    s.cfg.Host,
	}, nil
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: true, // 通过创建首个集合隐式建库
		domain.CapAlterDatabase:  false,
		domain.CapCreateTable:    true, // 语义为创建集合
		domain.CapAlterTable:     true, // 语义为修改集合校验器/索引
		domain.CapDropObject:     true,
		domain.CapIndexes:        true,
		domain.CapForeignKeys:    false,
		domain.CapSchemas:        false,
		domain.CapSQL:            false,
		domain.CapMongoFilter:    true,
		domain.CapJSONTree:       true,
		domain.CapExplain:        true,
		domain.CapMultiResult:    false,
		domain.CapTransactions:   false, // 多文档事务需 session，一期不提供
		domain.CapDataEdit:       true,
	}
}

func mapOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "mongo.Open", err, "连接已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.WrapError(domain.CodeTimeout, "mongo.Open", err, "连接超时")
	}
	if code, message, ok := commandError(err); ok {
		if code == 13 || code == 18 {
			return domain.WrapError(domain.CodeAuthFailed, "mongo.Open", err, "认证失败: %s", message)
		}
	}
	return domain.WrapError(domain.CodeConnection, "mongo.Open", err, "无法连接 MongoDB 服务")
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "mongo.query", err, "操作已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.WrapError(domain.CodeTimeout, "mongo.query", err, "操作超时")
	}
	if code, message, ok := commandError(err); ok {
		switch code {
		case 13, 18:
			return domain.WrapError(domain.CodePermission, "mongo.query", err, "权限不足: %s", message)
		case 26, 27, 60:
			return domain.WrapError(domain.CodeNotFound, "mongo.query", err, "集合或数据库不存在: %s", message)
		case 48, 11000:
			return domain.WrapError(domain.CodeSchema, "mongo.query", err, "对象已存在或唯一键冲突: %s", message)
		default:
			return domain.WrapError(domain.CodeQuery, "mongo.query", err,
				"MongoDB 错误 %d: %s", code, message)
		}
	}
	return domain.WrapError(domain.CodeQuery, "mongo.query", err, "MongoDB 操作失败")
}

func commandError(err error) (int32, string, bool) {
	var ptr *mongo.CommandError
	if errors.As(err, &ptr) && ptr != nil {
		return ptr.Code, ptr.Message, true
	}
	var value mongo.CommandError
	if errors.As(err, &value) {
		return value.Code, value.Message, true
	}
	return 0, "", false
}
