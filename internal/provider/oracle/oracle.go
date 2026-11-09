// Package oracle 是 Oracle Database 垂直业务模块（纯 Go go-ora 驱动，无需 Instant Client）。
// 树形浏览器中的「数据库」层级映射为 Oracle 的 schema（owner），对象名使用 "OWNER"."TABLE" 限定。
package oracle

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	goora "github.com/sijms/go-ora/v2"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type providerImpl struct{}

func init() { provider.Register(providerImpl{}) }

func (providerImpl) Kind() domain.Kind { return domain.KindOracle }

func (providerImpl) Capabilities() domain.CapabilitySet { return capabilitySet() }

func buildDSN(cfg domain.ConnectionConfig) (string, error) {
	service := cfg.Params["service"]
	if service == "" {
		service = cfg.Params["SID"]
	}
	if service == "" {
		service = cfg.Database
	}
	if service == "" {
		return "", domain.NewError(domain.CodeValidation, "oracle.dsn",
			"请通过 --database 或 params.service 指定 Oracle 服务名 / SID")
	}
	options := make(map[string]string)
	for k, v := range cfg.Params {
		if strings.EqualFold(k, "service") || strings.EqualFold(k, "sid") {
			continue
		}
		options[k] = v
	}
	switch strings.ToLower(cfg.TLSMode) {
	case "", "false", "disabled":
		// 默认不启用 TCPS
	case "true", "preferred":
		options["ssl"] = "true"
	case "skip-verify":
		options["ssl"] = "true"
		options["ssl verify"] = "false"
	default:
		return "", domain.NewError(domain.CodeValidation, "oracle.dsn",
			"不支持的 TLS 模式 %q", cfg.TLSMode)
	}
	return goora.BuildUrl(cfg.Host, cfg.Port, service, cfg.User, cfg.Password, options), nil
}

func (providerImpl) Open(ctx context.Context, cfg domain.ConnectionConfig) (domain.Session, error) {
	url, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("oracle", url)
	if err != nil {
		return nil, domain.WrapError(domain.CodeConnection, "oracle.Open", err, "初始化连接池失败")
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.EffectiveTimeout())
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, sqlkit.MapError("oracle.Open", err)
	}
	return &session{db: db, cfg: cfg, caps: capabilitySet()}, nil
}

var ansi = sqlkit.ANSIQuoter

// ownerOf 确定对象所属 schema：优先使用显式 Schema，其次使用树节点的 Database。
func ownerOf(ref domain.ObjectRef) string {
	if ref.Schema != "" {
		return ref.Schema
	}
	return ref.Database
}

func qualified(ref domain.ObjectRef) (string, error) {
	owner := ownerOf(ref)
	if owner == "" {
		return ansi.Ident(ref.Name)
	}
	return ansi.Qualified(owner, ref.Name)
}

type session struct {
	db   *sql.DB
	cfg  domain.ConnectionConfig
	caps domain.CapabilitySet
}

func (s *session) Kind() domain.Kind                  { return domain.KindOracle }
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
	return &queryExecutor{
		db: s.db,
		QueryRunner: sqlkit.QueryRunner{
			DB:     s.db,
			Op:     "oracle.query",
			MapErr: s.mapErr,
			Dialect: sqlkit.SelectDialect{
				Quoter:    ansi,
				Qualified: qualified,
				// Oracle 12c+ 的 ANSI 分页语法；与 WHERE / ORDER BY 可组合。
				LimitSuffix: func(limit int) string {
					return " FETCH FIRST " + strconv.Itoa(limit) + " ROWS ONLY"
				},
			},
		},
	}
}

func (s *session) Data() domain.DataEditor {
	return sqlkit.Editor{
		DB:        s.db,
		Quoter:    ansi,
		Qualified: qualified,
		Bind:      sqlkit.ColonNamedBind(),
		Op:        "oracle.edit",
		MapErr:    s.mapErr,
	}
}

func (s *session) Maintenance() domain.TableMaintenance {
	return sqlkit.Maintenance{Driver: &ddlDriver{db: s.db}}
}

func (s *session) mapErr(err error) error { return sqlkit.MapError("oracle.query", err) }

func (s *session) ServerInfo(ctx context.Context) (domain.ServerInfo, error) {
	var version string
	if err := s.db.QueryRowContext(ctx,
		"SELECT banner FROM v$version WHERE ROWNUM = 1").Scan(&version); err != nil {
		// 部分受限账号没有 v$version 权限，降级为当前用户标识。
		var user string
		if fallback := s.db.QueryRowContext(ctx, "SELECT USER FROM DUAL").Scan(&user); fallback == nil {
			version = "Oracle (当前用户 " + user + ")"
		} else {
			version = "Oracle"
		}
	}
	return domain.ServerInfo{Kind: domain.KindOracle, Version: version, Host: s.cfg.Host}, nil
}

func capabilitySet() domain.CapabilitySet {
	return domain.CapabilitySet{
		domain.CapCreateDatabase: false, // Oracle 的「库」是 schema/user，不在此创建
		domain.CapAlterDatabase:  false,
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
