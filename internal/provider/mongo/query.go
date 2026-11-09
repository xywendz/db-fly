package mongo

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/dbclient/dbclient/internal/domain"
)

type queryExecutor struct {
	client *mongo.Client
}

type querySpec struct {
	Collection string
	Filter     any
	Pipeline   []any
	Projection any
	Sort       any
	Limit      int64
	Skip       int64
}

// parseQuerySpec 支持两种输入：
//   - filter 模式：直接写 {"age":{"$gt":18}}，或 {"collection":"t","filter":{...},"sort":{...}}
//   - pipeline 模式：直接写 [{"$match":...}]，或 {"collection":"t","pipeline":[...]}
func parseQuerySpec(q domain.Query) (*querySpec, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" && q.Ref.Name != "" {
		text = "{}"
		if q.Collection == "" {
			q.Collection = q.Ref.Name
		}
	}
	if text == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query", "查询内容不能为空")
	}
	spec := &querySpec{
		Collection: q.Collection,
		Filter:     bson.M{},
		Limit:      int64(q.MaxRows) + 1, // 多取一行，让上层标记「结果被截断」
	}
	if q.MaxRows <= 0 {
		spec.Limit = 0
	}

	if q.Language == domain.LangMongoPipeline {
		if strings.HasPrefix(text, "[") {
			pipeline, err := parseJSONArray(text)
			if err != nil {
				return nil, err
			}
			spec.Pipeline = pipeline
			return spec, nil
		}
		doc, err := parseExtJSON(text)
		if err != nil {
			return nil, err
		}
		switch p := doc["pipeline"].(type) {
		case []any:
			spec.Pipeline = p
		case bson.A:
			spec.Pipeline = []any(p)
		default:
			return nil, domain.NewError(domain.CodeValidation, "mongo.query",
				"聚合查询需要 pipeline 数组")
		}
		applyMeta(spec, doc)
		return spec, nil
	}

	doc, err := parseExtJSON(text)
	if err != nil {
		return nil, err
	}
	if _, hasMeta := doc["filter"]; hasMeta {
		spec.Filter = doc["filter"]
		applyMeta(spec, doc)
	} else if _, hasMeta := doc["collection"]; hasMeta {
		applyMeta(spec, doc)
	} else {
		spec.Filter = doc
	}
	if spec.Collection == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query",
			"请通过 --collection / collection 字段指定集合")
	}
	return spec, nil
}

func applyMeta(spec *querySpec, doc bson.M) {
	if c, ok := doc["collection"].(string); ok && spec.Collection == "" {
		spec.Collection = c
	}
	if v, ok := doc["projection"]; ok {
		spec.Projection = v
	}
	if v, ok := doc["sort"]; ok {
		spec.Sort = v
	}
	if v, ok := numberAsInt64(doc["skip"]); ok {
		spec.Skip = v
	}
	if v, ok := numberAsInt64(doc["limit"]); ok && v > 0 {
		spec.Limit = v
	}
}

func (e *queryExecutor) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if q.Language != "" && q.Language != domain.LangMongoFilter && q.Language != domain.LangMongoPipeline {
		return nil, domain.NewError(domain.CodeUnsupported, "mongo.query",
			"MongoDB 模块只支持 JSON 查询，收到 %q", q.Language)
	}
	spec, err := parseQuerySpec(q)
	if err != nil {
		return nil, err
	}
	if spec.Collection == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query", "未指定集合")
	}
	if q.Database == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query", "未指定数据库")
	}
	coll := e.client.Database(q.Database).Collection(spec.Collection)

	var cursor *mongo.Cursor
	if spec.Pipeline != nil {
		cursor, err = coll.Aggregate(ctx, spec.Pipeline)
	} else {
		opts := options.Find()
		if spec.Limit > 0 {
			opts.SetLimit(spec.Limit)
		}
		if spec.Skip > 0 {
			opts.SetSkip(spec.Skip)
		}
		if spec.Projection != nil {
			opts.SetProjection(spec.Projection)
		}
		if spec.Sort != nil {
			opts.SetSort(spec.Sort)
		}
		cursor, err = coll.Find(ctx, spec.Filter, opts)
	}
	if err != nil {
		return nil, mapQueryError(err)
	}
	return newMongoResultSet(ctx, cursor)
}

func (e *queryExecutor) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	spec, err := parseQuerySpec(q)
	if err != nil {
		return nil, err
	}
	if q.Database == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.explain", "未指定数据库")
	}
	command := bson.M{
		"explain": bson.M{
			"find":   spec.Collection,
			"filter": spec.Filter,
		},
		"verbosity": "executionStats",
	}
	raw, err := e.client.Database(q.Database).RunCommand(ctx, command).Raw()
	if err != nil {
		return nil, mapQueryError(err)
	}
	return newSingleDocResultSet(raw)
}

// Exec 在指定库上执行原生 MongoDB 命令（JSON 文档）。
func (e *queryExecutor) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	doc, err := parseExtJSON(statement)
	if err != nil {
		return domain.ExecResult{}, err
	}
	if database == "" {
		database = "admin"
	}
	if err := e.client.Database(database).RunCommand(ctx, doc).Err(); err != nil {
		return domain.ExecResult{}, mapQueryError(err)
	}
	return domain.ExecResult{
		Statement: statement,
		Message:   "MongoDB 命令执行成功",
	}, nil
}
