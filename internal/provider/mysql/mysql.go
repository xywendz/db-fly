// Package mysql 是 MySQL 垂直业务模块。
// 本包自包含连接、元数据、方言 DDL、查询与结果映射，不依赖其他数据库模块。
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"

	gmysql "github.com/go-sql-driver/mysql"

	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/provider"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindMySQL }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	dsn, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "mysql.Open", err, "初始化连接池失败")
	}
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
	c := gmysql.NewConfig()
	c.User = cfg.User
	c.Passwd = cfg.Password
	c.Net = "tcp"
	c.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	c.DBName = cfg.Database
	c.ParseTime = false
	c.MultiStatements = false
	c.Timeout = cfg.EffectiveTimeout()
	// 语句执行超时统一由 context 控制，这里不设置 ReadTimeout 以免截断长查询。
	c.Params = map[string]string{"charset": "utf8mb4"}
	for k, v := range cfg.Params {
		c.Params[k] = v
	}
	switch cfg.TLSMode {
	case "", "false", "disabled":
		// 不启用 TLS
	case "true", "skip-verify", "preferred":
		c.TLSConfig = cfg.TLSMode
	default:
		return "", domain.NewError(domain.CodeValidation, "mysql.dsn",
			"不支持的 TLS 模式 %q（可用: false/true/skip-verify/preferred）", cfg.TLSMode)
	}
	return c.FormatDSN(), nil
}

// session 持有独立连接池，是运行期隔离的最小单元。
type session struct {
	db   *sql.DB
	cfg  domain.ConnectionConfig
	caps domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindMySQL }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Metadata() domain.MetadataBrowser   { return &metadataBrowser{db: s.db} }
func (s *session) Schema() domain.SchemaEditor        { return &schemaEditor{db: s.db} }
func (s *session) Query() domain.QueryExecutor        { return &queryExecutor{db: s.db, cfg: s.cfg} }
func (s *session) Data() domain.DataEditor            { return &dataEditor{db: s.db} }
func (s *session) Close() error                       { return s.db.Close() }

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	var version string
	if err := s.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return domain.ServerInfo{}, domain.WrapError(domain.CodeQuery, "mysql.ServerInfo", err, "读取服务器版本失败")
	}
	return domain.ServerInfo{
		Kind:    domain.KindMySQL,
		Version: version,
		Host:    s.cfg.Host,
	}, nil
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
		domain.CapSchemas:        false, // MySQL 的 database 与 schema 是同一层
		domain.CapSQL:            true,
		domain.CapExplain:        true,
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
		return domain.WrapError(domain.CodeCanceled, "mysql.Open", err, "连接已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return domain.WrapError(domain.CodeTimeout, "mysql.Open", err, "连接超时")
	}
	var me *gmysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1044, 1045:
			return domain.WrapError(domain.CodeAuthFailed, "mysql.Open", err, "认证失败: %s", me.Message)
		case 1049:
			return domain.WrapError(domain.CodeNotFound, "mysql.Open", err, "默认数据库不存在: %s", me.Message)
		default:
			return domain.WrapError(domain.CodeConnection, "mysql.Open", err, "服务器拒绝连接: %s", me.Message)
		}
	}
	return domain.WrapError(domain.CodeConnection, "mysql.Open", err, "无法连接 MySQL 服务器")
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return domain.WrapError(domain.CodeCanceled, "mysql.query", err, "查询已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return domain.WrapError(domain.CodeTimeout, "mysql.query", err, "查询超时")
	}
	var me *gmysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1044, 1045, 1142, 1227:
			return domain.WrapError(domain.CodePermission, "mysql.query", err, "权限不足: %s", me.Message)
		case 1049, 1054, 1146:
			return domain.WrapError(domain.CodeNotFound, "mysql.query", err, "对象不存在: %s", me.Message)
		case 1064:
			return domain.WrapError(domain.CodeQuery, "mysql.query", err, "SQL 语法错误: %s", me.Message)
		default:
			return domain.WrapError(domain.CodeQuery, "mysql.query", err, "MySQL 错误 %d: %s", me.Number, me.Message)
		}
	}
	return domain.WrapError(domain.CodeQuery, "mysql.query", err, "查询执行失败")
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
