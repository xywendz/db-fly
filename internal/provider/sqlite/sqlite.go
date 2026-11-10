// Package sqlite 是 SQLite 垂直业务模块（modernc.org/sqlite，纯 Go，无需 CGO）。
// 连接配置中的 Host 即数据库文件路径（也可用 params.file 指定，":memory:" 表示内存库）。
package sqlite

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindSQLite }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func buildDSN(cfg domain.ConnectionConfig) (string, error) {
	path := cfg.Params["file"]
	if path == "" {
		path = cfg.Host
	}
	if strings.TrimSpace(path) == "" {
		return "", domain.NewError(domain.CodeValidation, "sqlite.dsn",
			"请在主机栏（或 params.file）填写 SQLite 数据库文件路径")
	}
	if path == ":memory:" {
		return "file:sqlitememory?mode=memory&cache=shared&_pragma=busy_timeout(5000)", nil
	}
	if strings.Contains(path, "?") {
		return path, nil
	}
	return path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", nil
}

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	connStr, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "sqlite.Open", err, "初始化连接池失败")
	}
	// SQLite 单写连接，避免多连接并发导致 database is locked。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, sqlkit.MapError("sqlite.Open", err)
	}
	return &session{db: db, cfg: cfg, caps: capabilitySet()}, nil
}

var ansi = sqlkit.ANSIQuoter

// qualified 忽略默认的 main 库层级；附加库使用 db.table 形式。
func qualified(ref domain.ObjectRef) (string, error) {
	if ref.Database != "" && ref.Database != "main" {
		return ansi.Qualified(ref.Database, ref.Name)
	}
	return ansi.Ident(ref.Name)
}

type session struct {
	db   *sql.DB
	cfg  domain.ConnectionConfig
	caps domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindSQLite }
func (s *session) Capabilities() domain.CapabilitySet { return s.caps.Clone() }
func (s *session) Close() error                       { return s.db.Close() }
func (s *session) Metadata() domain.MetadataBrowser   { return &metadataBrowser{db: s.db} }

func (s *session) Schema() domain.SchemaEditor {
	return sqlkit.Schema{
		Driver: &ddlDriver{db: s.db},
		Runner: sqlkit.ScriptRunner{DB: s.db, MapErr: s.mapErr},
	}
}

func (s *session) Query() domain.QueryExecutor {
	return sqlkit.QueryRunner{
		DB:     s.db,
		Op:     "sqlite.query",
		MapErr: s.mapErr,
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
		ExplainPrefix: "EXPLAIN QUERY PLAN ",
	}
}

func (s *session) Data() domain.DataEditor {
	return sqlkit.Editor{
		DB:        s.db,
		Quoter:    ansi,
		Qualified: qualified,
		Bind:      sqlkit.QuestionMarkBind(),
		Op:        "sqlite.edit",
		MapErr:    s.mapErr,
	}
}

func (s *session) Maintenance() domain.TableMaintenance {
	return sqlkit.Maintenance{Driver: &ddlDriver{db: s.db}}
}

func (s *session) mapErr(err error) error { return sqlkit.MapError("sqlite.query", err) }

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	var version string
	if err := s.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return domain.ServerInfo{}, s.mapErr(err)
	}
	return domain.ServerInfo{
		Kind: domain.KindSQLite, Version: version, Edition: "SQLite", Host: s.cfg.Host,
	}, nil
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: false, // SQLite 的「库」是文件，通过 ATTACH 管理
		domain.CapAlterDatabase:  false,
		domain.CapCreateTable:    true,
		domain.CapAlterTable:     true,
		domain.CapDropObject:     true,
		domain.CapIndexes:        true,
		domain.CapForeignKeys:    true,
		domain.CapSchemas:        false,
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

// trimSemicolon 去掉 sqlite_master.sql 末尾的分号，便于展示与复制。
func trimSemicolon(s string) string { return strings.TrimSuffix(strings.TrimSpace(s), ";") }
