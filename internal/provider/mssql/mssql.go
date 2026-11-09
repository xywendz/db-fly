// Package mssql 是 SQL Server 垂直业务模块。
// 自包含连接、sys.* 元数据、T-SQL DDL、GO 拆批与多结果集，不依赖其他数据库模块。
package mssql

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	gsmssql "github.com/microsoft/go-mssqldb"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/provider"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindSQLServer }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	dsn, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	connector, err := gsmssql.NewConnector(dsn)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "sqlserver.Open", err, "连接串无效")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, mapOpenError(err)
	}
	return &session{db: db, cfg: cfg, caps: capabilitySet()}, nil
}

func buildDSN(cfg domain.ConnectionConfig) (string, error) {
	u := &url.URL{
		Scheme: "sqlserver",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
	}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	q := url.Values{}
	q.Set("connection timeout", strconv.Itoa(int(cfg.EffectiveTimeout().Seconds())))
	if cfg.Database != "" {
		q.Set("database", cfg.Database)
	}
	switch cfg.TLSMode {
	case "", "false", "disabled":
		q.Set("encrypt", "disable")
	case "true":
		q.Set("encrypt", "true")
	case "skip-verify":
		q.Set("encrypt", "true")
		q.Set("trustservercertificate", "true")
	case "preferred":
		q.Set("encrypt", "disable")
	default:
		return "", domain.NewError(domain.CodeValidation, "sqlserver.dsn",
			"不支持的 TLS 模式 %q（可用: false/true/skip-verify）", cfg.TLSMode)
	}
	for k, v := range cfg.Params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// session 持有独立连接池，是运行期隔离的最小单元。
type session struct {
	db   *sql.DB
	cfg  domain.ConnectionConfig
	caps domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindSQLServer }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Metadata() domain.MetadataBrowser   { return &metadataBrowser{db: s.db} }
func (s *session) Schema() domain.SchemaEditor        { return &schemaEditor{db: s.db} }
func (s *session) Query() domain.QueryExecutor        { return &queryExecutor{db: s.db, cfg: s.cfg} }
func (s *session) Data() domain.DataEditor            { return &dataEditor{db: s.db} }
func (s *session) Close() error                       { return s.db.Close() }

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	var version, edition string
	if err := s.db.QueryRowContext(ctx,
		"SELECT @@VERSION, ISNULL(CAST(SERVERPROPERTY('Edition') AS nvarchar(256)), '')").Scan(&version, &edition); err != nil {
		return domain.ServerInfo{}, domain.WrapError(domain.CodeQuery, "sqlserver.ServerInfo", err, "读取服务器版本失败")
	}
	return domain.ServerInfo{
		Kind:    domain.KindSQLServer,
		Version: firstLine(version),
		Edition: edition,
		Host:    s.cfg.Host,
	}, nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: true,
		domain.CapAlterDatabase:  true,
		domain.CapCreateTable:    true,
		domain.CapAlterTable:     true,
		domain.CapDropObject:     true,
		domain.CapIndexes:        true,
		domain.CapForeignKeys:    true,
		domain.CapSchemas:        true, // database.schema.table 三层
		domain.CapSQL:            true,
		domain.CapExplain:        true, // SET SHOWPLAN_ALL
		domain.CapMultiResult:    true,
		domain.CapTransactions:   true,
		domain.CapDataEdit:       true,
	}
}

func mapOpenError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "sqlserver.Open", err, "连接已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return domain.WrapError(domain.CodeTimeout, "sqlserver.Open", err, "连接超时")
	}
	if code, message, ok := serverError(err); ok {
		switch code {
		case 18456, 18452:
			return domain.WrapError(domain.CodeAuthFailed, "sqlserver.Open", err, "认证失败: %s", message)
		case 4060:
			return domain.WrapError(domain.CodeNotFound, "sqlserver.Open", err, "默认数据库不存在: %s", message)
		default:
			return domain.WrapError(domain.CodeConnection, "sqlserver.Open", err, "服务器拒绝连接: %s", message)
		}
	}
	return domain.WrapError(domain.CodeConnection, "sqlserver.Open", err, "无法连接 SQL Server")
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "sqlserver.query", err, "查询已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return domain.WrapError(domain.CodeTimeout, "sqlserver.query", err, "查询超时")
	}
	if code, message, ok := serverError(err); ok {
		switch code {
		case 18456, 18452:
			return domain.WrapError(domain.CodeAuthFailed, "sqlserver.query", err, "认证失败: %s", message)
		case 229, 262, 297:
			return domain.WrapError(domain.CodePermission, "sqlserver.query", err, "权限不足: %s", message)
		case 208, 2812, 4060:
			return domain.WrapError(domain.CodeNotFound, "sqlserver.query", err, "对象不存在: %s", message)
		case 102, 207:
			return domain.WrapError(domain.CodeQuery, "sqlserver.query", err, "T-SQL 语法/列名错误: %s", message)
		default:
			return domain.WrapError(domain.CodeQuery, "sqlserver.query", err, "SQL Server 错误 %d: %s", code, message)
		}
	}
	return domain.WrapError(domain.CodeQuery, "sqlserver.query", err, "语句执行失败")
}

func serverError(err error) (int32, string, bool) {
	var se gsmssql.Error
	if errors.As(err, &se) {
		return se.Number, se.Message, true
	}
	return 0, "", false
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
