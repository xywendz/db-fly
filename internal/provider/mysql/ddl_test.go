package mysql

import (
	"context"
	"strings"
	"testing"

	"github.com/dbclient/dbclient/internal/domain"
)

func TestCreateDatabaseScript(t *testing.T) {
	e := &schemaEditor{}
	script, err := e.CreateDatabaseScript(context.Background(), domain.DatabaseSpec{
		Name: "demo", Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci", IfNotExists: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE DATABASE IF NOT EXISTS `demo` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
	if len(script.Statements) != 1 || script.Statements[0] != want {
		t.Fatalf("脚本不符: %#v", script.Statements)
	}
	if len(script.Rollback) != 1 || script.Rollback[0] != "DROP DATABASE `demo`" {
		t.Errorf("回滚脚本不正确: %#v", script.Rollback)
	}
}

func TestCreateDatabaseRejectsInjection(t *testing.T) {
	e := &schemaEditor{}
	_, err := e.CreateDatabaseScript(context.Background(), domain.DatabaseSpec{
		Name: "demo", Charset: "utf8mb4; DROP DATABASE demo",
	})
	if err == nil || !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("应拒绝非法字符集，实际: %v", err)
	}
}

func TestQuoteIdentEscapesBacktick(t *testing.T) {
	got, err := quoteIdent("we`ird")
	if err != nil {
		t.Fatal(err)
	}
	if got != "`we``ird`" {
		t.Fatalf("标识符转义错误: %s", got)
	}
}

func TestCreateTableScript(t *testing.T) {
	e := &schemaEditor{}
	def := domain.TableDefinition{
		Ref: domain.ObjectRef{Database: "demo", Name: "users"},
		Columns: []domain.ColumnSpec{
			{Name: "id", Type: "BIGINT", PrimaryKey: true, AutoIncrement: true},
			{Name: "name", Type: "VARCHAR(128)", Nullable: false},
		},
		Indexes: []domain.IndexSpec{
			{Name: "uk_name", Columns: []string{"name"}, Unique: true},
		},
		Engine: "InnoDB", Charset: "utf8mb4", Comment: "用户表", IfNotExists: true,
	}
	script, err := e.CreateTableScript(context.Background(), def)
	if err != nil {
		t.Fatal(err)
	}
	stmt := script.Statements[0]
	for _, part := range []string{
		"CREATE TABLE IF NOT EXISTS `demo`.`users`",
		"`id` BIGINT NOT NULL AUTO_INCREMENT",
		"PRIMARY KEY (`id`)",
		"UNIQUE INDEX `uk_name` (`name`)",
		"ENGINE = InnoDB",
		"COMMENT = '用户表'",
	} {
		if !strings.Contains(stmt, part) {
			t.Errorf("建表脚本缺少 %q:\n%s", part, stmt)
		}
	}
}

func TestAlterTableScriptAndRollback(t *testing.T) {
	e := &schemaEditor{}
	ref := domain.ObjectRef{Database: "demo", Name: "users"}
	changes := []domain.SchemaChange{
		{Kind: domain.ChangeAddColumn, Column: domain.ColumnSpec{Name: "age", Type: "INT", Nullable: true}},
		{Kind: domain.ChangeAddIndex, Index: domain.IndexSpec{Name: "idx_age", Columns: []string{"age"}}},
		{Kind: domain.ChangeDropColumn, Column: domain.ColumnSpec{Name: "legacy"}},
	}
	script, err := e.AlterTableScript(context.Background(), ref, changes)
	if err != nil {
		t.Fatal(err)
	}
	stmt := script.Statements[0]
	for _, part := range []string{
		"ADD COLUMN `age` INT NULL", "ADD INDEX `idx_age` (`age`)", "DROP COLUMN `legacy`",
	} {
		if !strings.Contains(stmt, part) {
			t.Errorf("ALTER 脚本缺少 %q:\n%s", part, stmt)
		}
	}
	if len(script.Rollback) != 2 {
		t.Fatalf("回滚语句数 = %d, 期望 2: %#v", len(script.Rollback), script.Rollback)
	}
	if !strings.Contains(script.Rollback[0], "DROP INDEX") {
		t.Error("回滚顺序应逆序，优先删除新增索引")
	}
	if len(script.Warnings) == 0 {
		t.Error("删除字段应给出不可自动回滚的警告")
	}
}

func TestColumnDefinitionDefaultLiteral(t *testing.T) {
	def, err := columnDefinition(domain.ColumnSpec{
		Name: "note", Type: "VARCHAR(64)", Nullable: true,
		Default: strPtr("it's ok"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "DEFAULT 'it''s ok'") {
		t.Errorf("字符串默认值未转义: %s", def)
	}
	def, err = columnDefinition(domain.ColumnSpec{
		Name: "n", Type: "INT", Default: strPtr("42"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "DEFAULT 42") {
		t.Errorf("数字默认值不应加引号: %s", def)
	}
}

func TestColumnDefinitionRejectsDangerousType(t *testing.T) {
	_, err := columnDefinition(domain.ColumnSpec{Name: "x", Type: "INT; DROP TABLE t"})
	if err == nil {
		t.Fatal("应拒绝含分号的字段类型")
	}
}

func TestCapabilitySet(t *testing.T) {
	caps := capabilitySet()
	if !caps.Has(domain.CapCreateDatabase) || !caps.Has(domain.CapSQL) {
		t.Error("MySQL 应声明建库与 SQL 能力")
	}
	if caps.Has(domain.CapSchemas) || caps.Has(domain.CapMongoFilter) {
		t.Error("MySQL 不应声明 schema 层级或 Mongo 能力")
	}
}

func strPtr(s string) *string { return &s }
