package mssql

import (
	"context"
	"strings"
	"testing"

	"github.com/dbclient/dbclient/internal/domain"
)

func strPtr(s string) *string { return &s }

func TestSplitBatches(t *testing.T) {
	text := "SELECT 1\nGO\nSELECT 2\n-- 注释中的 GO 不应拆批\nGO\nSELECT 3"
	batches := splitBatches(text)
	if len(batches) != 3 {
		t.Fatalf("GO 拆批数量 = %d, 期望 3: %#v", len(batches), batches)
	}
	if !strings.Contains(batches[1], "SELECT 2") || !strings.Contains(batches[1], "GO 不应拆批") {
		t.Errorf("批内容被错误切分: %q", batches[1])
	}
	if len(splitBatches("  \n GO \n ")) != 0 {
		t.Error("空批不应出现在结果中")
	}
}

func TestQuoteQualifiedAndDefaultSchema(t *testing.T) {
	got, err := quoteQualified("db", "sales", "order]x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "[db].[sales].[order]]x]" {
		t.Fatalf("标识符转义错误: %q", got)
	}
	if defaultSchema("") != "dbo" || defaultSchema("ods") != "ods" {
		t.Error("默认 schema 推断错误")
	}
	if _, err := quoteIdent("bad\x00name"); err == nil {
		t.Error("含 NUL 的标识符应被拒绝")
	}
}

func TestBatchClassification(t *testing.T) {
	for _, batch := range []string{
		"SELECT * FROM t", "/* 注释 */ WITH cte AS (SELECT 1) SELECT * FROM cte",
		"-- 注释\nEXEC sp_who", "SET SHOWPLAN_ALL ON",
	} {
		if !isResultBatch(batch) {
			t.Errorf("%q 应判定为有结果集的批", batch)
		}
	}
	for _, batch := range []string{"INSERT INTO t VALUES (1)", "CREATE TABLE t (id int)"} {
		if isResultBatch(batch) {
			t.Errorf("%q 不应判定为有结果集的批", batch)
		}
	}
}

func TestCapabilities(t *testing.T) {
	caps := capabilitySet()
	for _, cap := range []domain.Capability{
		domain.CapSchemas, domain.CapMultiResult, domain.CapExplain,
		domain.CapSQL, domain.CapDataEdit,
	} {
		if !caps.Has(cap) {
			t.Errorf("缺少能力 %s", cap)
		}
	}
	if caps.Has(domain.CapJSONTree) {
		t.Error("关系型模块不应声明 JSON 树能力")
	}
}

func TestDataEditorPreviewUsesNamedParameters(t *testing.T) {
	editor := &dataEditor{}
	req := domain.EditRequest{
		Ref: domain.ObjectRef{Database: "db", Schema: "sales", Name: "t"},
		Edits: []domain.RowEdit{{
			Kind:   domain.EditUpdate,
			Key:    map[string]*string{"id": strPtr("1")},
			Values: map[string]*string{"name": strPtr("O'Brien")},
		}},
	}
	previews, err := editor.Preview(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(previews, "\n")
	for _, want := range []string{"USE [db]", "[sales].[t]", "N'O''Brien'"} {
		if !strings.Contains(joined, want) {
			t.Errorf("预览缺少 %q: %s", want, joined)
		}
	}

	built, err := editor.build(req)
	if err != nil {
		t.Fatal(err)
	}
	parameterized := built[0].sql
	if !strings.Contains(parameterized, "@p1") || !strings.Contains(parameterized, "@p2") {
		t.Errorf("参数化 SQL 未使用命名参数: %s", parameterized)
	}
	if strings.Contains(parameterized, "O'Brien") || len(built[0].args) != 2 {
		t.Errorf("用户值不应拼接进 SQL: %s / %d 个参数", parameterized, len(built[0].args))
	}

	req.Edits = []domain.RowEdit{
		{Kind: domain.EditDelete, Key: map[string]*string{"id": strPtr("2")}},
		{Kind: domain.EditInsert, Values: map[string]*string{"name": nil}},
	}
	previews, err = editor.Preview(context.Background(), req)
	if err != nil || len(previews) < 3 {
		t.Fatalf("删除/插入预览失败: %#v / %v", previews, err)
	}
	joined = strings.Join(previews, "\n")
	if !strings.Contains(joined, "DELETE FROM") || !strings.Contains(joined, "INSERT INTO") {
		t.Errorf("删除/插入语句未生成: %s", joined)
	}
	if !strings.Contains(joined, "NULL") {
		t.Error("NULL 值预览丢失")
	}
}
