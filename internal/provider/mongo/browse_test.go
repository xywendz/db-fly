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
