// Package elasticsearch 是 Elasticsearch 垂直业务模块。
// 自包含 REST 客户端、索引/模板元数据、JSON DSL 查询、命令式 DDL 与 _bulk 回写，
// 不依赖任何第三方 SDK，也不 import 其他数据库模块。
package elasticsearch

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindElasticsearch }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	c, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	var root struct {
		ClusterName string `json:"cluster_name"`
		Version     struct {
			Number       string `json:"number"`
			Distribution string `json:"distribution"`
		} `json:"version"`
	}
	if _, err := c.jsonCall(ctx, http.MethodGet, "/", nil, &root); err != nil {
		c.Close()
		return nil, mapOpenError(err)
	}
	c.setVersion(root.Version.Number, root.Version.Distribution)
	return &session{
		client: c, cfg: cfg, caps: capabilitySet(),
		version: root.Version.Number, cluster: root.ClusterName,
	}, nil
}

// session 持有独立的 HTTP 连接池与连接配置。
type session struct {
	client  *esClient
	cfg     domain.ConnectionConfig
	caps    domain.CapabilitySet
	version string
	cluster string
}

func (s *session) Kind() domain.Kind                  { return domain.KindElasticsearch }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Metadata() domain.MetadataBrowser {
	return &metadataBrowser{client: s.client, cfg: s.cfg}
}
func (s *session) Schema() domain.SchemaEditor          { return &schemaEditor{client: s.client, cfg: s.cfg} }
func (s *session) Query() domain.QueryExecutor          { return &queryExecutor{client: s.client, cfg: s.cfg} }
func (s *session) Data() domain.DataEditor              { return &dataEditor{client: s.client, cfg: s.cfg} }
func (s *session) Maintenance() domain.TableMaintenance { return nil }
func (s *session) Close() error                         { s.client.Close(); return nil }

func (s *session) ServerInfo(context.Context) (domain.ServerInfo, error) {
	return domain.ServerInfo{
		Kind:    domain.KindElasticsearch,
		Version: s.version,
		Edition: s.cluster,
		Host:    s.cfg.Host,
	}, nil
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: true, // 创建组合索引模板（逻辑库）
		domain.CapAlterDatabase:  true, // 覆盖索引模板
		domain.CapCreateTable:    true, // 语义为创建索引
		domain.CapAlterTable:     true, // 语义为 put mapping / put settings
		domain.CapDropObject:     true,
		domain.CapIndexes:        true, // mapping 字段即索引
		domain.CapForeignKeys:    false,
		domain.CapSchemas:        false,
		domain.CapSQL:            false,
		domain.CapMongoFilter:    false,
		domain.CapKeyValues:      false,
		domain.CapESDSL:          true,
		domain.CapJSONTree:       true,
		domain.CapExplain:        true,
		domain.CapMultiResult:    true, // 聚合桶作为第二结果集
		domain.CapTransactions:   false,
		domain.CapDataEdit:       true,
		domain.CapTableFilter:    true,
		domain.CapShowCreate:     true,
		domain.CapTruncateTable:  false,
		domain.CapEmptyTable:     false,
	}
}

func mapOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "es.open", err, "连接已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.WrapError(domain.CodeTimeout, "es.open", err, "连接超时")
	}
	var he *esHTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case http.StatusUnauthorized:
			return domain.WrapError(domain.CodeAuthFailed, "es.open", err, "认证失败: %s", he.Reason)
		case http.StatusForbidden:
			return domain.WrapError(domain.CodePermission, "es.open", err, "权限不足: %s", he.Reason)
		default:
			return domain.WrapError(domain.CodeConnection, "es.open", err,
				"无法连接 Elasticsearch 服务: %s", he.Reason)
		}
	}
	return domain.WrapError(domain.CodeConnection, "es.open", err, "无法连接 Elasticsearch 服务")
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "es.query", err, "操作已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.WrapError(domain.CodeTimeout, "es.query", err, "操作超时")
	}
	var he *esHTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case http.StatusUnauthorized:
			return domain.WrapError(domain.CodeAuthFailed, "es.query", err, "认证失败: %s", he.Reason)
		case http.StatusForbidden:
			return domain.WrapError(domain.CodePermission, "es.query", err, "权限不足: %s", he.Reason)
		case http.StatusNotFound:
			return domain.WrapError(domain.CodeNotFound, "es.query", err,
				"索引或资源不存在: %s", he.Reason)
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return domain.WrapError(domain.CodeTimeout, "es.query", err, "请求超时或触发限流: %s", he.Reason)
		case http.StatusBadRequest, http.StatusConflict:
			if strings.Contains(he.Type, "already_exists") || strings.Contains(he.Type, "version_conflict") {
				return domain.WrapError(domain.CodeSchema, "es.query", err, "对象已存在或版本冲突: %s", he.Reason)
			}
			return domain.WrapError(domain.CodeValidation, "es.query", err, "请求无效: %s", he.Reason)
		default:
			return domain.WrapError(domain.CodeQuery, "es.query", err, "Elasticsearch 错误: %s", he.Reason)
		}
	}
	return domain.WrapError(domain.CodeQuery, "es.query", err, "Elasticsearch 操作失败")
}
