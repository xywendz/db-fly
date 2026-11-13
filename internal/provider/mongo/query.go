package mongo

import (
	"context"
	"encoding/json"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/db-fly/db-fly/internal/domain"
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
	// 表数据浏览的分页偏移（关系型由方言生成 OFFSET，Mongo 直接透传 Skip）。
	if q.Offset > 0 {
		spec.Skip = int64(q.Offset)
	}

	if q.Language == domain.LangMongoPipeline {
		if strings.HasPrefix(text, "[") {
			pipeline, err := parseJSONArray(text)
			if err != nil {
				return nil, err
			}
			spec.Pipeline = pipeline
			return spec, applyBrowseConditions(spec, q)
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
		return spec, applyBrowseConditions(spec, q)
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
	return spec, applyBrowseConditions(spec, q)
}

// applyBrowseConditions 把表数据浏览面板中的过滤/排序覆盖到查询上。
// 结构化条件（Conditions / OrderTerms）优先于自由文本 filter / sort JSON；
// 两者都不存在时保持原样（通常是全文扫描或文档内嵌的 filter）。
func applyBrowseConditions(spec *querySpec, q domain.Query) error {
	hasStructured := len(q.Conditions) > 0 || len(q.OrderTerms) > 0
	hasText := strings.TrimSpace(q.Filter) != "" || strings.TrimSpace(q.Sort) != ""
	// 聚合管道模式不允许再叠加浏览过滤/排序，应直接写在 pipeline 里。
	if spec.Pipeline != nil && (hasStructured || hasText) {
		return domain.NewError(domain.CodeValidation, "mongo.query",
			"聚合管道模式下请直接在 pipeline 中使用 $match / $sort 阶段")
	}
	// 结构化条件优先（与关系型 sqlkit.BuildConditions 行为一致）。
	if len(q.Conditions) > 0 {
		filter, err := buildMongoFilterFromConditions(q.Conditions)
		if err != nil {
			return err
		}
		spec.Filter = filter
	} else if filter := strings.TrimSpace(q.Filter); filter != "" {
		doc, err := parseExtJSON(filter)
		if err != nil {
			return domain.WrapError(domain.CodeValidation, "mongo.query", err,
				"表数据浏览的 filter 不是合法 JSON")
		}
		spec.Filter = doc
	}
	if len(q.OrderTerms) > 0 {
		sortDoc, err := buildMongoSortFromTerms(q.OrderTerms)
		if err != nil {
			return err
		}
		spec.Sort = sortDoc
	} else if sortText := strings.TrimSpace(q.Sort); sortText != "" {
		doc, err := parseExtJSON(sortText)
		if err != nil {
			return domain.WrapError(domain.CodeValidation, "mongo.query", err,
				"表数据浏览的 sort 不是合法 JSON")
		}
		spec.Sort = doc
	}
	return nil
}

// buildMongoFilterFromConditions 把结构化过滤条件转成 Mongo find 的 BSON 过滤器。
// 各条件通过 Join 与上一条连接，OR 作为分组边界：A AND B OR C 译为 (A∧B)∨C。
func buildMongoFilterFromConditions(conditions []domain.FilterCondition) (any, error) {
	var groups [][]bson.M
	var current []bson.M
	for i, c := range conditions {
		expr, err := conditionToBSON(c)
		if err != nil {
			return nil, err
		}
		join := "AND"
		if i > 0 {
			join = strings.ToUpper(strings.TrimSpace(c.Join))
			if join == "" {
				join = "AND"
			}
		}
		if join == "OR" && len(current) > 0 {
			groups = append(groups, current)
			current = nil
		}
		current = append(current, expr)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	if len(groups) == 0 {
		return bson.M{}, nil
	}
	if len(groups) == 1 {
		return mergeExprs(groups[0]), nil
	}
	orExpr := make([]bson.M, 0, len(groups))
	for _, g := range groups {
		orExpr = append(orExpr, mergeExprs(g))
	}
	return bson.M{"$or": orExpr}, nil
}

func mergeExprs(exprs []bson.M) bson.M {
	merged := bson.M{}
	for _, e := range exprs {
		for k, v := range e {
			merged[k] = v
		}
	}
	return merged
}

// conditionToBSON 把单条结构化条件转成 BSON 表达式（一个字段上的匹配）。
func conditionToBSON(c domain.FilterCondition) (bson.M, error) {
	column := strings.TrimSpace(c.Column)
	if column == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query", "过滤条件缺少字段")
	}
	op := strings.ToUpper(strings.TrimSpace(c.Operator))
	if op == "" {
		op = "="
	}
	value := strings.TrimSpace(c.Value)
	switch op {
	case "IS NULL":
		return bson.M{column: nil}, nil
	case "IS NOT NULL":
		return bson.M{column: bson.M{"$ne": nil}}, nil
	case "IN", "NOT IN":
		arr, err := parseFilterArray(value)
		if err != nil {
			return nil, err
		}
		if len(arr) == 0 {
			return nil, domain.NewError(domain.CodeValidation, "mongo.query",
				"IN 条件 %q 至少需要一个值", column)
		}
		if op == "IN" {
			return bson.M{column: bson.M{"$in": arr}}, nil
		}
		return bson.M{column: bson.M{"$nin": arr}}, nil
	}
	if value == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.query",
			"条件 %q 需要填写值", column)
	}
	v := parseFilterLiteral(value)
	// _id 列优先按 ObjectId 处理（用户输入 24 位十六进制串时）。
	if column == "_id" {
		if oid, err := bson.ObjectIDFromHex(value); err == nil {
			v = oid
		}
	}
	switch op {
	case "=", "==":
		return bson.M{column: v}, nil
	case "!=", "<>":
		return bson.M{column: bson.M{"$ne": v}}, nil
	case ">":
		return bson.M{column: bson.M{"$gt": v}}, nil
	case ">=":
		return bson.M{column: bson.M{"$gte": v}}, nil
	case "<":
		return bson.M{column: bson.M{"$lt": v}}, nil
	case "<=":
		return bson.M{column: bson.M{"$lte": v}}, nil
	case "LIKE", "NOT LIKE":
		re := likeToRegexPattern(value)
		if op == "LIKE" {
			return bson.M{column: bson.M{"$regex": re, "$options": "i"}}, nil
		}
		return bson.M{column: bson.M{"$not": bson.M{"$regex": re, "$options": "i"}}}, nil
	}
	return nil, domain.NewError(domain.CodeValidation, "mongo.query",
		"不支持的操作符 %q", c.Operator)
}

// parseFilterLiteral 把条件值文本还原为合适类型：数字/布尔/null 走 JSON，
// 其余保持字符串，使过滤值与文档真实类型一致。
func parseFilterLiteral(text string) any {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(t), &v); err == nil {
		return v
	}
	return t
}

// parseFilterArray 把 IN 条件的值（逗号分隔）逐个解析为字面量。
func parseFilterArray(text string) ([]any, error) {
	parts := strings.Split(text, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, parseFilterLiteral(p))
	}
	return out, nil
}

// likeToRegexPattern 把 SQL LIKE 模式转成正则：% → .*，_ → .，并转义其余正则元字符。
func likeToRegexPattern(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; ch {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		case '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '\\', '^', '$', '|':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return b.String()
}

// buildMongoSortFromTerms 把结构化排序项转成 Mongo 的 sort 文档（1 升序 / -1 降序）。
func buildMongoSortFromTerms(terms []domain.SortTerm) (any, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	doc := bson.D{}
	for _, t := range terms {
		col := strings.TrimSpace(t.Column)
		if col == "" {
			return nil, domain.NewError(domain.CodeValidation, "mongo.query", "排序条件缺少字段")
		}
		dir := 1
		if t.Desc {
			dir = -1
		}
		doc = append(doc, bson.E{Key: col, Value: dir})
	}
	return doc, nil
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
