// Package postgres 是 PostgreSQL 垂直业务模块（纯 Go pgx 驱动）。
// 按库维护独立连接池，因为 PostgreSQL 不允许跨库限定名。
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindPostgreSQL }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

// dsn 按 database 生成连接串；PoolSet 浏览其它库时会再次调用。
func dsn(cfg domain.ConnectionConfig, database string) (string, error) {
	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
	}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	if database != "" {
		u.Path = "/" + database
	}
	q := url.Values{}
	switch strings.ToLower(cfg.TLSMode) {
	case "", "false", "disabled":
		q.Set("sslmode", "disable")
	case "true":
		q.Set("sslmode", "require")
	case "skip-verify":
		q.Set("sslmode", "require")
	case "preferred":
		q.Set("sslmode", "prefer")
	default:
		return "", domain.NewError(domain.CodeValidation, "postgresql.dsn",
			"不支持的 TLS 模式 %q（可用: false/true/skip-verify/preferred）", cfg.TLSMode)
	}
	q.Set("connect_timeout", strconv.Itoa(int(cfg.EffectiveTimeout().Seconds())))
	q.Set("application_name", "db-fly")
	for k, v := range cfg.Params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	connStr, err := dsn(cfg, cfg.Database)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "postgresql.Open", err, "初始化连接池失败")
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
	pools := sqlkit.NewPoolSet("pgx", cfg.Database, db, func(database string) (string, error) {
		return dsn(cfg, database)
	})
	return &session{cfg: cfg, pools: pools, caps: capabilitySet()}, nil
}

var ansi = sqlkit.ANSIQuoter

// splitRef 兼容树形浏览器把非 public schema 编码成 "schema.table" 的形式。
func splitRef(ref domain.ObjectRef) (schema, table string) {
	schema, table = ref.Schema, ref.Name
	if schema == "" {
		if i := strings.IndexByte(table, '.'); i > 0 && i < len(table)-1 {
			schema, table = table[:i], table[i+1:]
		}
	}
	return schema, table
}

func schemaOf(ref domain.ObjectRef) string {
	schema, _ := splitRef(ref)
	if schema == "" {
		return "public"
	}
	return schema
}

func qualified(ref domain.ObjectRef) (string, error) {
	schema, table := splitRef(ref)
	if schema == "" {
		return ansi.Ident(table)
	}
	return ansi.Qualified(schema, table)
}

// session 通过 PoolSet 按库取连接池，并把通用 sqlkit 组件按 PostgreSQL 方言装配成端口。
type session struct {
	cfg   domain.ConnectionConfig
	pools *sqlkit.PoolSet
	caps  domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindPostgreSQL }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Close() error                       { return s.pools.Close() }
func (s *session) Metadata() domain.MetadataBrowser {
	return &metadataBrowser{cfg: s.cfg, pools: s.pools}
}

func (s *session) Schema() domain.SchemaEditor {
	driver := &ddlDriver{cfg: s.cfg, pools: s.pools}
	return sqlkit.Schema{
		Driver: driver,
		Runner: sqlkit.ScriptRunner{DBFor: s.pools.DB, MapErr: mapQueryError},
		CreateDatabaseStmt: func(spec domain.DatabaseSpec) (string, error) {
			name, err := ansi.Ident(spec.Name)
			if err != nil {
				return "", err
			}
			if err := sqlkit.SafeToken(spec.Charset, "编码"); err != nil {
				return "", err
			}
			stmt := "CREATE DATABASE " + name
			if spec.Charset != "" {
				stmt += " ENCODING '" + sqlkit.EscapeLiteral(spec.Charset) + "'"
			}
			return stmt, nil
		},
		AlterDatabaseStmt: func(name string, spec domain.DatabaseSpec) (string, error) {
			quoted, err := ansi.Ident(name)
			if err != nil {
				return "", err
			}
			if err := sqlkit.SafeToken(spec.Charset, "编码"); err != nil {
				return "", err
			}
			if spec.Charset == "" {
				return "", domain.NewError(domain.CodeValidation, "postgresql.script",
					"请填写客户端编码（charset）；PostgreSQL 的排序规则在建库时确定，不能在此修改")
			}
			return "ALTER DATABASE " + quoted + " SET client_encoding TO '" +
				sqlkit.EscapeLiteral(spec.Charset) + "'", nil
		},
	}
}

func (s *session) Query() domain.QueryExecutor {
	return sqlkit.QueryRunner{
		DBFor:  s.pools.DB,
		Op:     "postgresql.query",
		MapErr: mapQueryError,
		Dialect: sqlkit.SelectDialect{
			Quoter:    ansi,
			Qualified: qualified,
			LimitSuffix: func(limit, offset int) string {
				if offset > 0 {
					return " LIMIT " + strconv.Itoa(limit) + " OFFSET " + strconv.Itoa(offset)
				}
				return " LIMIT " + strconv.Itoa(limit)
			},
		},
	}
}

func (s *session) Data() domain.DataEditor {
	return sqlkit.Editor{
		DBFor:     s.pools.DB,
		Quoter:    ansi,
		Qualified: qualified,
		Bind:      sqlkit.DollarBind(),
		Op:        "postgresql.edit",
		MapErr:    mapQueryError,
	}
}

func (s *session) Maintenance() domain.TableMaintenance {
	return sqlkit.Maintenance{Driver: &ddlDriver{cfg: s.cfg, pools: s.pools}}
}

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	db, err := s.pools.DB(ctx, "")
	if err != nil {
		return domain.ServerInfo{}, err
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		return domain.ServerInfo{}, mapQueryError(err)
	}
	return domain.ServerInfo{
		Kind: domain.KindPostgreSQL, Version: version, Host: s.cfg.Host,
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
		domain.CapSchemas:        true,
		domain.CapSQL:            true,
		domain.CapExplain:        true,
		domain.CapTransactions:   true,
		domain.CapDataEdit:       true,
		domain.CapTableFilter:    true,
		domain.CapShowCreate:     true,
		domain.CapTruncateTable:  true,
		domain.CapEmptyTable:     true,
	}
}

func mapOpenError(err error) error {
	if err == nil {
		return nil
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "28P01", "28000":
			return domain.WrapError(domain.CodeAuthFailed, "postgresql.Open", err, "认证失败: %s", pe.Message)
		case "3D000":
			return domain.WrapError(domain.CodeNotFound, "postgresql.Open", err, "默认数据库不存在: %s", pe.Message)
		default:
			return domain.WrapError(domain.CodeConnection, "postgresql.Open", err, "服务器拒绝连接: %s", pe.Message)
		}
	}
	return sqlkit.MapError("postgresql.Open", err)
}

func mapQueryError(err error) error {
	if err == nil {
		return nil
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "28P01", "28000":
			return domain.WrapError(domain.CodeAuthFailed, "postgresql.query", err, "认证失败: %s", pe.Message)
		case "42501":
			return domain.WrapError(domain.CodePermission, "postgresql.query", err, "权限不足: %s", pe.Message)
		case "42P01", "42703", "3D000":
			return domain.WrapError(domain.CodeNotFound, "postgresql.query", err, "对象不存在: %s", pe.Message)
		case "42601":
			return domain.WrapError(domain.CodeQuery, "postgresql.query", err, "SQL 语法错误: %s", pe.Message)
		default:
			return domain.WrapError(domain.CodeQuery, "postgresql.query", err,
				"PostgreSQL 错误 %s: %s", pe.Code, pe.Message)
		}
	}
	return sqlkit.MapError("postgresql.query", err)
}
