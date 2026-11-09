package mongo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dbclient/dbclient/internal/domain"
)

func strPtr(s string) *string { return &s }

func TestParseQuerySpecFilter(t *testing.T) {
	spec, err := parseQuerySpec(domain.Query{
		Language: domain.LangMongoFilter, Text: `{"age":{"$gt":18}}`,
		Collection: "users", MaxRows: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Collection != "users" || spec.Limit != 11 || len(spec.Pipeline) != 0 {
		t.Fatalf("filter 规格错误: %+v", spec)
	}
	if _, err := parseQuerySpec(domain.Query{Text: `{"a":1}`}); err == nil {
		t.Error("缺少集合名应报错")
	}
}

func TestParseQuerySpecMetaAndPipeline(t *testing.T) {
	spec, err := parseQuerySpec(domain.Query{
		Text: `{"collection":"orders","filter":{"status":"A"},"sort":{"total":-1}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Collection != "orders" || spec.Filter == nil || spec.Sort == nil {
		t.Fatalf("带元信息的 filter 解析错误: %+v", spec)
	}

	pipeline, err := parseQuerySpec(domain.Query{
		Language: domain.LangMongoPipeline, Collection: "orders",
		Text: `[{"$match":{"status":"A"}},{"$count":"n"}]`,
	})
	if err != nil || len(pipeline.Pipeline) != 2 {
		t.Fatalf("pipeline 解析错误: %+v / %v", pipeline, err)
	}

	byObject, err := parseQuerySpec(domain.Query{
		Language: domain.LangMongoPipeline, Collection: "orders",
		Text: `{"pipeline":[{"$match":{"a":1}}]}`,
	})
	if err != nil || len(byObject.Pipeline) != 1 {
		t.Fatalf("pipeline 对象解析错误: %+v / %v", byObject, err)
	}

	preview, err := parseQuerySpec(domain.Query{
		Ref: domain.ObjectRef{Database: "db", Name: "orders"},
	})
	if err != nil || preview.Collection != "orders" {
		t.Fatalf("对象预览回退错误: %+v / %v", preview, err)
	}
}

func TestFlattenPathsAndLookup(t *testing.T) {
	doc := map[string]any{
		"a":   1,
		"b":   map[string]any{"c": "x", "d": map[string]any{"e": true}},
		"arr": []any{1, 2},
	}
	paths := make([]string, 0)
	flattenPaths(doc, "", 0, &paths, map[string]bool{})
	joined := strings.Join(paths, ",")
	for _, want := range []string{"a", "b.c", "b.d.e", "arr"} {
		if !strings.Contains(joined, want) {
			t.Errorf("列式投影缺少路径 %q: %s", want, joined)
		}
	}
	if value, ok := lookupPath(doc, "b.c"); !ok || value != "x" {
		t.Errorf("嵌套路径取值错误: %v / %v", value, ok)
	}
	if _, ok := lookupPath(doc, "b.missing"); ok {
		t.Error("不存在的路径不应命中")
	}
}

func TestJSONSchemaFromColumns(t *testing.T) {
	validator := jsonSchemaFromColumns([]domain.ColumnSpec{
		{Name: "age", Type: "INT", Nullable: false},
		{Name: "ok", Type: "BOOL", Nullable: true},
		{Name: "name", Type: "VARCHAR", Nullable: true},
	})
	schema, ok := validator["$jsonSchema"].(map[string]any)
	if !ok || schema["bsonType"] != "object" {
		t.Fatalf("$jsonSchema 结构错误: %#v", validator)
	}
	props := schema["properties"].(map[string]any)
	if props["age"].(map[string]any)["bsonType"] != "number" ||
		props["ok"].(map[string]any)["bsonType"] != "bool" ||
		props["name"].(map[string]any)["bsonType"] != "string" {
		t.Errorf("BSON 类型映射错误: %#v", props)
	}
	required := schema["required"].([]any)
	if len(required) != 1 || required[0] != "age" {
		t.Errorf("必填字段推断错误: %#v", required)
	}
}

func TestEncodeDecodeCommand(t *testing.T) {
	text, err := encodeCommand(mongoCommand{
		Action: "createCollection", Database: "db", Collection: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := decodeCommand(text)
	if err != nil || cmd.Action != "createCollection" || cmd.Collection != "c" {
		t.Fatalf("命令往返失败: %+v / %v", cmd, err)
	}
	if _, err := decodeCommand(`{"database":"db"}`); err == nil {
		t.Error("缺少 action 的命令应报错")
	}
	if _, err := decodeCommand(`not-json`); err == nil {
		t.Error("非法 JSON 命令应报错")
	}
}

func TestDataEditorPreviewCommands(t *testing.T) {
	editor := &dataEditor{}
	req := domain.EditRequest{
		Ref: domain.ObjectRef{Database: "db", Name: "c"},
		Edits: []domain.RowEdit{
			{
				Kind:   domain.EditUpdate,
				Key:    map[string]*string{"_id": strPtr("507f1f77bcf86cd799439011")},
				Values: map[string]*string{"name": strPtr("Ada"), "age": strPtr("36")},
			},
			{Kind: domain.EditDelete, Key: map[string]*string{"_id": strPtr("507f1f77bcf86cd799439012")}},
			{Kind: domain.EditInsert, Values: map[string]*string{"name": strPtr("Bob")}},
		},
	}
	previews, err := editor.Preview(context.Background(), req)
	if err != nil || len(previews) != 3 {
		t.Fatalf("Mongo 编辑预览失败: %#v / %v", previews, err)
	}
	var update map[string]any
	if err := json.Unmarshal([]byte(previews[0]), &update); err != nil {
		t.Fatal(err)
	}
	if _, ok := update["updateOne"]; !ok || update["collection"] != "c" {
		t.Errorf("updateOne 命令结构错误: %#v", update)
	}
	if !strings.Contains(previews[1], "deleteOne") || !strings.Contains(previews[2], "insertOne") {
		t.Errorf("delete/insert 命令缺失: %s / %s", previews[1], previews[2])
	}
}

func TestCapabilities(t *testing.T) {
	caps := capabilitySet()
	for _, cap := range []domain.Capability{
		domain.CapMongoFilter, domain.CapJSONTree, domain.CapDataEdit,
	} {
		if !caps.Has(cap) {
			t.Errorf("缺少能力 %s", cap)
		}
	}
	if caps.Has(domain.CapSQL) || caps.Has(domain.CapMultiResult) {
		t.Error("文档型模块不应声明 SQL/多结果集能力")
	}
}
