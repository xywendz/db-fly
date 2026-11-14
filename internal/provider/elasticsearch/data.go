package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

type dataEditor struct {
	client *esClient
	cfg    domain.ConnectionConfig
}

func (e *dataEditor) Preview(ctx context.Context, req domain.EditRequest) ([]string, error) {
	if len(req.Edits) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "es.edit", "没有待提交的变更")
	}
	physical, err := e.target(req.Ref)
	if err != nil {
		return nil, err
	}
	typeName := e.resolveTypeName(ctx, physical)
	out := make([]string, 0, len(req.Edits))
	for i, edit := range req.Edits {
		lines, err := bulkLines(edit, physical, rawDocAt(req, i), typeName)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return out, nil
}

// Apply 把一批结构化变更组装成一次 _bulk 请求；一批 edits = 一个 NDJSON 请求，
// 与导入向导的「每批行数」天然对齐。ES 没有跨文档事务，逐条动作各自原子。
func (e *dataEditor) Apply(ctx context.Context, req domain.EditRequest) (domain.EditResult, error) {
	if len(req.Edits) == 0 {
		return domain.EditResult{}, domain.NewError(domain.CodeValidation, "es.edit", "没有待提交的变更")
	}
	physical, err := e.target(req.Ref)
	if err != nil {
		return domain.EditResult{}, err
	}
	typeName := e.resolveTypeName(ctx, physical)
	var body bytes.Buffer
	statements := make([]string, 0, minInt(20, len(req.Edits)))
	for i, edit := range req.Edits {
		lines, err := bulkLines(edit, physical, rawDocAt(req, i), typeName)
		if err != nil {
			return domain.EditResult{}, err
		}
		for _, line := range lines {
			body.WriteString(line)
			body.WriteByte('\n')
		}
		if len(statements) < 20 {
			statements = append(statements, strings.Join(lines, "\n"))
		}
	}
	refresh := strings.ToLower(strings.TrimSpace(e.cfg.Params["refresh"]))
	if refresh != "false" && refresh != "wait_for" {
		refresh = "true"
	}
	var resp struct {
		Errors bool             `json:"errors"`
		Took   int              `json:"took"`
		Items  []map[string]any `json:"items"`
	}
	if _, err := e.client.ndjsonCall(ctx, http.MethodPost,
		"/_bulk?refresh="+refresh, body.Bytes(), &resp); err != nil {
		return domain.EditResult{Statements: statements}, mapQueryError(err)
	}
	applied, failures := inspectBulkResponse(resp.Errors, resp.Items, len(req.Edits))
	result := domain.EditResult{Applied: applied, Statements: statements}
	if len(failures) > 0 {
		return result, domain.NewError(domain.CodeQuery, "es.edit",
			"bulk 写入有 %d 条失败: %s", len(failures), strings.Join(failures[:minInt(3, len(failures))], "; "))
	}
	return result, nil
}

func (e *dataEditor) target(ref domain.ObjectRef) (string, error) {
	physical := physicalIndex(e.cfg, ref.Database, ref.Name)
	if err := validateIndexExpr(physical); err != nil {
		return "", err
	}
	return physical, nil
}

// resolveTypeName 返回该索引在 _bulk 动作里需要的 _type：
// ES 7+ / OpenSearch 已移除类型，返回空串；ES 5.x/6.x 需从索引 mapping 取出真实类型
// （每个索引的类型可能不同，不能用全局默认值），取不到时回退到 "doc"。
func (e *dataEditor) resolveTypeName(ctx context.Context, physical string) string {
	if e.client.isOpenSearch() || e.client.majorVersion() >= 7 {
		return ""
	}
	var raw map[string]any
	if _, err := e.client.jsonCall(ctx, http.MethodGet, "/"+physical+"/_mapping", nil, &raw); err != nil {
		return "doc"
	}
	mappings := asMap(asMap(asMap(raw)[physical])["mappings"])
	for t := range mappings {
		return t
	}
	return "doc"
}

func inspectBulkResponse(reportedErrors bool, items []map[string]any, total int) (int, []string) {
	// 部分兼容实现不返回 items；此时以服务端的 errors 标志为准。
	if len(items) == 0 {
		if reportedErrors {
			return 0, []string{"服务端报告 bulk 失败但未提供逐条错误"}
		}
		return total, nil
	}
	applied := 0
	var failures []string
	for _, item := range items {
		for action, detail := range item {
			d := asMap(detail)
			status := numberFromAny(d["status"])
			if cause := d["error"]; cause != nil || status >= 400 {
				failures = append(failures, fmt.Sprintf("%s(_id=%v, status=%d): %v",
					action, d["_id"], status, cause))
				continue
			}
			applied++
		}
	}
	return applied, failures
}

// bulkLines 把一条 RowEdit 转成 _bulk 的 1~2 行 NDJSON。
// rawDoc 非空时（JSON/NDJSON 导入）直接写入原始文档，保留标量类型与嵌套结构。
func bulkLines(edit domain.RowEdit, physical, rawDoc, typeName string) ([]string, error) {
	switch edit.Kind {
	case domain.EditInsert:
		var source any
		id := stringValue(edit.Values, "_id")
		if rawDoc != "" {
			doc, docID, err := rawSource(rawDoc)
			if err != nil {
				return nil, err
			}
			source = doc
			if docID != "" {
				id = docID
			}
		} else {
			source = sourceFromValues(edit.Values)
		}
		action := map[string]any{"_index": physical}
		if typeName != "" {
			action["_type"] = typeName
		}
		if id != "" {
			action["_id"] = id
		}
		head, err := encodeLine(map[string]any{"index": action})
		if err != nil {
			return nil, err
		}
		doc, err := encodeLine(source)
		if err != nil {
			return nil, err
		}
		return []string{head, doc}, nil

	case domain.EditUpdate:
		id := stringValue(edit.Key, "_id")
		if id == "" {
			id = stringValue(edit.Values, "_id")
		}
		if id == "" {
			return nil, domain.NewError(domain.CodeValidation, "es.edit",
				"更新文档必须提供 _id 键值")
		}
		doc := sourceFromValues(edit.Values)
		if len(doc) == 0 {
			return nil, domain.NewError(domain.CodeValidation, "es.edit", "更新内容为空")
		}
		um := map[string]any{"_index": physical, "_id": id, "retry_on_conflict": 3}
		if typeName != "" {
			um["_type"] = typeName
		}
		head, err := encodeLine(map[string]any{"update": um})
		if err != nil {
			return nil, err
		}
		payload, err := encodeLine(map[string]any{"doc": doc})
		if err != nil {
			return nil, err
		}
		return []string{head, payload}, nil

	case domain.EditDelete:
		id := stringValue(edit.Key, "_id")
		if id == "" {
			id = stringValue(edit.Values, "_id")
		}
		if id == "" {
			return nil, domain.NewError(domain.CodeValidation, "es.edit",
				"删除文档必须提供 _id 键值")
		}
		dm := map[string]any{"_index": physical, "_id": id}
		if typeName != "" {
			dm["_type"] = typeName
		}
		head, err := encodeLine(map[string]any{"delete": dm})
		if err != nil {
			return nil, err
		}
		return []string{head}, nil

	default:
		return nil, domain.NewError(domain.CodeUnsupported, "es.edit",
			"不支持的编辑类型 %q", edit.Kind)
	}
}

func rawDocAt(req domain.EditRequest, i int) string {
	if i < len(req.Documents) {
		return strings.TrimSpace(req.Documents[i])
	}
	return ""
}

// rawSource 从原始文档文本还原 _source，并把 _id 提取为 ES 元数据。
func rawSource(text string) (map[string]any, string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, "", domain.WrapError(domain.CodeValidation, "es.edit", err, "原始文档不是合法 JSON 对象")
	}
	id := ""
	if value, ok := doc["_id"]; ok && value != nil {
		if _, complex := value.(map[string]any); complex {
			return nil, "", domain.NewError(domain.CodeValidation, "es.edit", "_id 必须是标量")
		}
		id = fmt.Sprint(value)
	}
	delete(doc, "_id")
	return doc, id, nil
}

func encodeLine(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", domain.WrapError(domain.CodeValidation, "es.edit", err, "文档序列化失败")
	}
	return string(encoded), nil
}

func sourceFromValues(values map[string]*string) map[string]any {
	source := make(map[string]any, len(values))
	for key, value := range values {
		if key == "_id" {
			continue // _id 是 ES 元数据，不写入 _source
		}
		if value == nil {
			source[key] = nil
			continue
		}
		source[key] = parseScalar(*value)
	}
	return source
}

func stringValue(values map[string]*string, key string) string {
	if values == nil {
		return ""
	}
	value := values[key]
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
