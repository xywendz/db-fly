package elasticsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

type queryExecutor struct {
	client *esClient
	cfg    domain.ConnectionConfig
}

type searchSpec struct {
	Index string
	Body  map[string]any
}

// parseSearchSpec 支持完整 _search body、带 index 的包装对象、纯文本 KQL，
// 并叠加表数据浏览面板的结构化过滤/排序条件。
func parseSearchSpec(q domain.Query, cfg domain.ConnectionConfig) (*searchSpec, error) {
	text := strings.TrimSpace(q.Text)
	body := map[string]any{}
	index := strings.TrimSpace(q.Collection)
	if text != "" {
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			doc := asMap(parsed)
			if doc == nil {
				return nil, domain.NewError(domain.CodeValidation, "es.query",
					"DSL 必须是 JSON 对象（_search 请求体）")
			}
			if v, ok := doc["index"].(string); ok && index == "" {
				index = strings.TrimSpace(v)
			}
			for k, v := range doc {
				if k != "index" {
					body[k] = v
				}
			}
		} else {
			body["query"] = map[string]any{
				"query_string": map[string]any{"query": text},
			}
		}
	}
	if index == "" && strings.TrimSpace(q.Ref.Name) != "" {
		index = physicalIndex(cfg, q.Database, q.Ref.Name)
	}
	if index == "" {
		db := strings.TrimSpace(q.Database)
		sep := namespaceSeparator(cfg)
		if db == "" || db == defaultDatabase || sep == "" {
			index = "*,-.*"
		} else {
			index = db + sep + "*"
		}
	}
	if err := validateIndexExpr(index); err != nil {
		return nil, err
	}

	if len(q.Conditions) > 0 {
		filter, err := buildESFilter(q.Conditions)
		if err != nil {
			return nil, err
		}
		body["query"] = filter
	} else if filterText := strings.TrimSpace(q.Filter); filterText != "" {
		var parsed any
		if err := json.Unmarshal([]byte(filterText), &parsed); err == nil {
			if m := asMap(parsed); m != nil {
				if inner, ok := m["query"]; ok {
					body["query"] = inner
				} else {
					body["query"] = m
				}
			}
		} else {
			body["query"] = map[string]any{"query_string": map[string]any{"query": filterText}}
		}
	}
	if _, ok := body["query"]; !ok {
		body["query"] = map[string]any{"match_all": map[string]any{}}
	}
	if len(q.OrderTerms) > 0 {
		body["sort"] = buildESSort(q.OrderTerms)
	} else if sortText := strings.TrimSpace(q.Sort); sortText != "" {
		var parsed any
		if err := json.Unmarshal([]byte(sortText), &parsed); err == nil {
			body["sort"] = parsed
		} else {
			return nil, domain.NewError(domain.CodeValidation, "es.query",
				"排序条件必须是 JSON（如 [{\"created_at\":{\"order\":\"desc\"}}]）")
		}
	}
	if q.Offset > 0 {
		body["from"] = q.Offset
	}
	limit := q.MaxRows
	if limit <= 0 {
		limit = domain.DefaultMaxRows
	}
	if userSize := numberFromAny(body["size"]); userSize > 0 {
		body["size"] = minInt(int(userSize), limit+1)
	} else {
		body["size"] = limit + 1
	}
	return &searchSpec{Index: index, Body: body}, nil
}

// buildESFilter 把结构化条件编译成 bool 查询：A AND B OR C 译为 (A∧B)∨C。
func buildESFilter(conditions []domain.FilterCondition) (map[string]any, error) {
	var groups [][]map[string]any
	var current []map[string]any
	for i, c := range conditions {
		expr, err := conditionToES(c)
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
		return map[string]any{"match_all": map[string]any{}}, nil
	}
	if len(groups) == 1 {
		clauses := make([]any, 0, len(groups[0]))
		for _, expr := range groups[0] {
			clauses = append(clauses, expr)
		}
		if len(clauses) == 1 {
			return clauses[0].(map[string]any), nil
		}
		return map[string]any{"bool": map[string]any{"must": clauses}}, nil
	}
	branches := make([]any, 0, len(groups))
	for _, group := range groups {
		if len(group) == 1 {
			branches = append(branches, group[0])
			continue
		}
		clauses := make([]any, 0, len(group))
		for _, expr := range group {
			clauses = append(clauses, expr)
		}
		branches = append(branches, map[string]any{"bool": map[string]any{"must": clauses}})
	}
	return map[string]any{"bool": map[string]any{"should": branches, "minimum_should_match": 1}}, nil
}

func conditionToES(c domain.FilterCondition) (map[string]any, error) {
	column := strings.TrimSpace(c.Column)
	if column == "" {
		return nil, domain.NewError(domain.CodeValidation, "es.query", "过滤条件缺少字段")
	}
	op := strings.ToUpper(strings.TrimSpace(c.Operator))
	if op == "" {
		op = "="
	}
	value := strings.TrimSpace(c.Value)
	switch op {
	case "IS NULL":
		return map[string]any{"bool": map[string]any{
			"must_not": []any{map[string]any{"exists": map[string]any{"field": column}}},
		}}, nil
	case "IS NOT NULL":
		return map[string]any{"exists": map[string]any{"field": column}}, nil
	case "IN", "NOT IN":
		parts := strings.Split(value, ",")
		values := make([]any, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				values = append(values, parseScalar(p))
			}
		}
		if len(values) == 0 {
			return nil, domain.NewError(domain.CodeValidation, "es.query",
				"IN 条件 %q 至少需要一个值", column)
		}
		terms := map[string]any{"terms": map[string]any{column: values}}
		if op == "NOT IN" {
			return map[string]any{"bool": map[string]any{"must_not": []any{terms}}}, nil
		}
		return terms, nil
	}
	if value == "" {
		return nil, domain.NewError(domain.CodeValidation, "es.query",
			"条件 %q 需要填写值", column)
	}
	v := parseScalar(value)
	switch op {
	case "=", "==":
		return map[string]any{"match": map[string]any{column: v}}, nil
	case "!=", "<>":
		return map[string]any{"bool": map[string]any{
			"must_not": []any{map[string]any{"match": map[string]any{column: v}}},
		}}, nil
	case ">", ">=", "<", "<=":
		key := map[string]string{">": "gt", ">=": "gte", "<": "lt", "<=": "lte"}[op]
		return map[string]any{"range": map[string]any{column: map[string]any{key: v}}}, nil
	case "LIKE", "NOT LIKE":
		pattern := strings.ReplaceAll(strings.ReplaceAll(value, "%", "*"), "_", "?")
		if !strings.Contains(pattern, "*") {
			pattern = "*" + pattern + "*"
		}
		wildcard := map[string]any{"wildcard": map[string]any{column: map[string]any{"value": pattern}}}
		if op == "NOT LIKE" {
			return map[string]any{"bool": map[string]any{"must_not": []any{wildcard}}}, nil
		}
		return wildcard, nil
	}
	return nil, domain.NewError(domain.CodeValidation, "es.query",
		"不支持的操作符 %q", c.Operator)
}

func buildESSort(terms []domain.SortTerm) []any {
	out := make([]any, 0, len(terms))
	for _, t := range terms {
		column := strings.TrimSpace(t.Column)
		if column == "" {
			continue
		}
		order := "asc"
		if t.Desc {
			order = "desc"
		}
		out = append(out, map[string]any{column: map[string]any{"order": order}})
	}
	return out
}

func (e *queryExecutor) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if q.Language != "" && q.Language != domain.LangESDSL {
		return nil, domain.NewError(domain.CodeUnsupported, "es.query",
			"Elasticsearch 模块只支持 JSON DSL，收到 %q", q.Language)
	}
	spec, err := parseSearchSpec(q, e.cfg)
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if _, err := e.client.jsonCall(ctx, http.MethodPost,
		"/"+spec.Index+"/_search", spec.Body, &resp); err != nil {
		return nil, mapQueryError(err)
	}
	return newESResultSet(resp, spec.Index)
}

func (e *queryExecutor) Explain(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	spec, err := parseSearchSpec(q, e.cfg)
	if err != nil {
		return nil, err
	}
	spec.Body["explain"] = true
	spec.Body["size"] = 1
	var resp map[string]any
	if _, err := e.client.jsonCall(ctx, http.MethodPost,
		"/"+spec.Index+"/_search", spec.Body, &resp); err != nil {
		return nil, mapQueryError(err)
	}
	hits := asMap(resp["hits"])
	var explanation any
	for _, hit := range toSlice(hits["hits"]) {
		if v, ok := asMap(hit)["_explanation"]; ok {
			explanation = v
			break
		}
	}
	doc := map[string]any{
		"index":       spec.Index,
		"request":     spec.Body,
		"took":        resp["took"],
		"timed_out":   resp["timed_out"],
		"total":       hits["total"],
		"explanation": explanation,
	}
	if explanation == nil {
		doc["note"] = "查询没有命中文档，ES 仅在有命中时返回 _explanation"
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, domain.WrapError(domain.CodeQuery, "es.explain", err, "执行计划序列化失败")
	}
	return newSingleDocResultSet(string(encoded)), nil
}

// Exec 提交原生 REST 命令或 es_command 动作对象。
// REST 命令形如 {"method":"PUT","path":"/{index}/_settings","body":{...}}，
// path 中的 {index}/{database} 会由 database 参数替换。
func (e *queryExecutor) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(statement)), &raw); err != nil {
		return domain.ExecResult{}, domain.WrapError(domain.CodeValidation, "es.exec", err,
			"原生命令必须是 JSON 对象")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return domain.ExecResult{}, err
	}
	var cmd esCommand
	if err := json.Unmarshal(encoded, &cmd); err != nil {
		return domain.ExecResult{}, domain.WrapError(domain.CodeValidation, "es.exec", err,
			"原生命令字段类型无效")
	}
	if cmd.Database == "" {
		cmd.Database = database
	}
	text, err := encodeCommand(cmd)
	if err != nil {
		return domain.ExecResult{}, err
	}
	res, err := (&schemaEditor{client: e.client, cfg: e.cfg}).Apply(ctx, domain.Script{
		Format: scriptFormat, Statements: []string{text},
	})
	if err != nil {
		return domain.ExecResult{}, err
	}
	message := "Elasticsearch 命令执行成功"
	if len(res.Outcomes) > 0 {
		message = res.Outcomes[0].Message
	}
	return domain.ExecResult{
		Statement: truncate(statement), Message: message,
	}, nil
}
