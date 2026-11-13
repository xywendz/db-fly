package mongo

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/db-fly/db-fly/internal/domain"
)

func TestApplyBrowseConditions(t *testing.T) {
	spec := &querySpec{Collection: "users", Filter: bson.M{}}
	q := domain.Query{Filter: `{"age":{"$gt":18}}`, Sort: `{"age":-1}`}
	if err := applyBrowseConditions(spec, q); err != nil {
		t.Fatalf("应用浏览条件失败: %v", err)
	}
	filter, ok := spec.Filter.(bson.M)
	if !ok || filter["age"] == nil {
		t.Fatalf("filter 未按 JSON 解析: %#v", spec.Filter)
	}
	sortDoc, ok := spec.Sort.(bson.M)
	if !ok || sortDoc["age"] == nil {
		t.Fatalf("sort 未按 JSON 解析: %#v", spec.Sort)
	}
}

func TestApplyBrowseConditionsRejectsBadInput(t *testing.T) {
	if err := applyBrowseConditions(&querySpec{Collection: "users"}, domain.Query{Filter: "not json"}); err == nil {
		t.Fatal("非法 filter JSON 应报错")
	}
	if err := applyBrowseConditions(&querySpec{Collection: "users"}, domain.Query{Sort: "[1,2"}); err == nil {
		t.Fatal("非法 sort JSON 应报错")
	}
	pipeline := &querySpec{Collection: "users", Pipeline: []any{}}
	if err := applyBrowseConditions(pipeline, domain.Query{Filter: `{}`}); err == nil {
		t.Fatal("聚合管道模式下应提示使用 $match/$sort")
	}
}

func TestBuildMongoFilterFromConditions(t *testing.T) {
	// 单条件：数值字符串解析为数值类型。
	f, err := buildMongoFilterFromConditions([]domain.FilterCondition{
		{Column: "age", Operator: ">", Value: "18"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fm := f.(bson.M)
	gt, ok := fm["age"].(bson.M)
	if !ok || gt["$gt"] != float64(18) {
		t.Fatalf("数值条件解析错误: %#v", fm)
	}

	// AND 与 OR 混合：A AND B OR C 应译为 (A∧B)∨C。
	or, err := buildMongoFilterFromConditions([]domain.FilterCondition{
		{Column: "a", Operator: "=", Value: "1"},
		{Column: "b", Operator: "=", Value: "2", Join: "AND"},
		{Column: "c", Operator: "=", Value: "3", Join: "OR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	orM, ok := or.(bson.M)
	if !ok {
		t.Fatalf("OR 条件应生成 $or: %#v", or)
	}
	orArr, ok := orM["$or"].([]bson.M)
	if !ok || len(orArr) != 2 {
		t.Fatalf("$or 应包含两组: %#v", orM)
	}

	// 不等、IS NULL、LIKE、IN。
	mixed, err := buildMongoFilterFromConditions([]domain.FilterCondition{
		{Column: "name", Operator: "!=", Value: "x"},
		{Column: "note", Operator: "IS NULL"},
		{Column: "tag", Operator: "LIKE", Value: "a%"},
		{Column: "status", Operator: "IN", Value: "A,B,C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mm := mixed.(bson.M)
	if mm["name"].(bson.M)["$ne"] != "x" {
		t.Errorf("!= 解析错误: %#v", mm["name"])
	}
	if mm["note"] != nil {
		t.Errorf("IS NULL 解析错误: %#v", mm["note"])
	}
	if re := mm["tag"].(bson.M)["$regex"]; re != "^a.*$" {
		t.Errorf("LIKE 转正则错误: %#v", mm["tag"])
	}
	in := mm["status"].(bson.M)["$in"].([]any)
	if len(in) != 3 {
		t.Errorf("IN 解析错误: %#v", mm["status"])
	}
}

func TestBuildMongoSortFromTerms(t *testing.T) {
	s, err := buildMongoSortFromTerms([]domain.SortTerm{
		{Column: "age", Desc: false},
		{Column: "name", Desc: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sd := s.(bson.D)
	if len(sd) != 2 || sd[0].Key != "age" || sd[0].Value != 1 || sd[1].Key != "name" || sd[1].Value != -1 {
		t.Fatalf("排序项解析错误: %#v", sd)
	}
}

func TestApplyBrowseConditionsPrefersStructured(t *testing.T) {
	spec := &querySpec{Collection: "users", Filter: bson.M{}}
	q := domain.Query{
		Conditions: []domain.FilterCondition{{Column: "age", Operator: ">", Value: "18"}},
		OrderTerms: []domain.SortTerm{{Column: "age", Desc: true}},
	}
	if err := applyBrowseConditions(spec, q); err != nil {
		t.Fatalf("应用结构化条件失败: %v", err)
	}
	fm, ok := spec.Filter.(bson.M)
	if !ok || fm["age"].(bson.M)["$gt"] != float64(18) {
		t.Fatalf("结构化条件未生效: %#v", spec.Filter)
	}
	sd, ok := spec.Sort.(bson.D)
	if !ok || len(sd) != 1 || sd[0].Key != "age" || sd[0].Value != -1 {
		t.Fatalf("结构化排序未生效: %#v", spec.Sort)
	}
}
