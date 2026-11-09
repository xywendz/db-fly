//go:build integration

package mysql

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	gmysql "github.com/go-sql-driver/mysql"

	"github.com/db-fly/db-fly/internal/domain"
)

// 集成测试需要真实 MySQL。示例：
//   MYSQL_DSN='root:pass@tcp(127.0.0.1:3306)/' go test -tags integration ./internal/provider/mysql -v

func integrationConfig(t *testing.T) domain.ConnectionConfig {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 MYSQL_DSN，跳过 MySQL 集成测试")
	}
	parsed, err := gmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("MYSQL_DSN 无效: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Addr)
	if err != nil {
		t.Fatalf("DSN 地址无效 %q: %v", parsed.Addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("端口无效: %v", err)
	}
	return domain.ConnectionConfig{
		ID: "integration", Name: "integration", Kind: domain.KindMySQL,
		Host: host, Port: port, User: parsed.User, Password: parsed.Passwd,
		Database: parsed.DBName, TimeoutSec: 10,
	}
}

func TestIntegrationFullChain(t *testing.T) {
	cfg := integrationConfig(t)
	p := providerImpl{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	session, err := p.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer session.Close()

	if info, err := session.ServerInfo(ctx); err != nil || info.Version == "" {
		t.Fatalf("版本探测失败: %+v, %v", info, err)
	}

	dbName := fmt.Sprintf("db_fly_it_%d", time.Now().UnixNano()%1e9)
	createDB, err := session.Schema().CreateDatabaseScript(ctx, domain.DatabaseSpec{
		Name: dbName, Charset: "utf8mb4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Schema().Apply(ctx, createDB); err != nil {
		t.Fatalf("建库失败: %v", err)
	}
	defer func() {
		_, _ = session.Schema().Apply(context.Background(), domain.Script{
			Statements: []string{"DROP DATABASE IF EXISTS `" + dbName + "`"},
		})
	}()

	createTable, err := session.Schema().CreateTableScript(ctx, domain.TableDefinition{
		Ref: domain.ObjectRef{Database: dbName, Name: "items"},
		Columns: []domain.ColumnSpec{
			{Name: "id", Type: "BIGINT", PrimaryKey: true, AutoIncrement: true},
			{Name: "title", Type: "VARCHAR(64)"},
		},
		Engine: "InnoDB",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Schema().Apply(ctx, createTable); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	ref := domain.ObjectRef{Database: dbName, Name: "items"}
	schema, err := session.Metadata().Describe(ctx, ref)
	if err != nil {
		t.Fatalf("读取结构失败: %v", err)
	}
	if len(schema.Columns) != 2 || len(schema.Indexes) < 1 {
		t.Fatalf("结构不符合预期: %+v", schema)
	}
	if ddl, err := session.Schema().ShowCreate(ctx, ref); err != nil || ddl == "" {
		t.Fatalf("SHOW CREATE 失败: %q, %v", ddl, err)
	}

	if _, err := session.Query().Exec(ctx,
		"INSERT INTO `"+dbName+"`.`items` (`title`) VALUES ('hello'), ('world')", ""); err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	rs, err := session.Query().Execute(ctx, domain.Query{
		Language: domain.LangSQL,
		Text:     "SELECT id, title, NULL AS empty FROM `" + dbName + "`.`items` ORDER BY id",
	})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	defer rs.Close()
	count := 0
	for rs.Next() {
		count++
		row := rs.Row()
		if len(row) != 3 || !row[2].Null {
			t.Fatalf("结果行不正确: %+v", row)
		}
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("期望 2 行，实际 %d 行", count)
	}
}
