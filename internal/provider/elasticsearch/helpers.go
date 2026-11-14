package elasticsearch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/db-fly/db-fly/internal/domain"
)

const (
	maxProjectionColumns = 40
	sampleDocuments      = 50
	maxCellText          = 240
	defaultDatabase      = "default"
	templatePrefix       = "dbfly_"
	scriptFormat         = "es_command"
)

// esCommand 是 Script.Format = "es_command" 时每条 JSON 语句的结构。
// Options 承载与动作相关的请求体（索引模板 / create-index body / mapping / settings）。
type esCommand struct {
	Action      string         `json:"action"`
	Database    string         `json:"database,omitempty"`
	Collection  string         `json:"collection,omitempty"`
	Index       string         `json:"index,omitempty"`
	NewName     string         `json:"new_name,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
	Method      string         `json:"method,omitempty"`
	Path        string         `json:"path,omitempty"`
	Body        map[string]any `json:"body,omitempty"`
	IfNotExists bool           `json:"if_not_exists,omitempty"`
}

func encodeCommand(cmd esCommand) (string, error) {
	b, err := json.MarshalIndent(cmd, "", "  ")
	return string(b), err
}

func decodeCommand(text string) (esCommand, error) {
	var cmd esCommand
	if err := json.Unmarshal([]byte(text), &cmd); err != nil {
		return cmd, domain.WrapError(domain.CodeValidation, "es.command", err, "命令 JSON 无法解析")
	}
	if strings.TrimSpace(cmd.Action) == "" && strings.TrimSpace(cmd.Path) == "" {
		return cmd, domain.NewError(domain.CodeValidation, "es.command",
			"命令缺少 action 或 path 字段")
	}
	return cmd, nil
}

func commandScript(statements, rollbacks, warnings []string) domain.Script {
	return domain.Script{
		Format: scriptFormat, Statements: statements,
		Rollback: rollbacks, Warnings: warnings,
	}
}

// namespaceSeparator 返回逻辑库与集合之间的分隔符；显式设置为空表示不做命名空间分组。
func namespaceSeparator(cfg domain.ConnectionConfig) string {
	if v, ok := cfg.Params["namespaceSeparator"]; ok {
		v = strings.TrimSpace(v)
		// 前端空值会被表单删除，用显式 none/off 表示「不做命名空间分组」。
		if strings.EqualFold(v, "none") || strings.EqualFold(v, "off") {
			return ""
		}
		return v
	}
	return "."
}

// physicalIndex 把 (逻辑库, 相对集合名) 映射为 ES 物理索引名。
// 反方向由 splitPhysicalIndex 完成，映射规则只存在于本 provider 内。
func physicalIndex(cfg domain.ConnectionConfig, database, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	sep := namespaceSeparator(cfg)
	db := strings.TrimSpace(database)
	if db == "" || db == defaultDatabase || sep == "" {
		return name
	}
	prefix := db + sep
	if strings.HasPrefix(name, prefix) {
		return name
	}
	return prefix + name
}

func splitPhysicalIndex(cfg domain.ConnectionConfig, index string) (string, string) {
	sep := namespaceSeparator(cfg)
	if sep != "" {
		if i := strings.Index(index, sep); i > 0 && i < len(index)-len(sep) {
			return index[:i], index[i+len(sep):]
		}
	}
	return defaultDatabase, index
}

func isSystemIndex(name string) bool { return strings.HasPrefix(name, ".") }

func showSystemIndices(cfg domain.ConnectionConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Params["showSystemIndices"]), "true")
}

// validateName 校验创建类操作中的库名 / 索引名（ES 索引名必须小写且不含路径分隔符）。
func validateName(name, what string) error {
	if strings.TrimSpace(name) == "" {
		return domain.NewError(domain.CodeValidation, "es.name", "%s不能为空", what)
	}
	if len(name) > 200 {
		return domain.NewError(domain.CodeValidation, "es.name", "%s过长（最多 200 字符）", what)
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, "+") {
		return domain.NewError(domain.CodeValidation, "es.name", "%s不能以 . - _ + 开头: %q", what, name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == '+' {
			continue
		}
		return domain.NewError(domain.CodeValidation, "es.name",
			"%s只能包含小写字母、数字与 . _ - +: %q", what, name)
	}
	return nil
}

// validateIndexExpr 校验查询目标表达式（允许通配符、多索引与负向排除，禁止路径注入）。
func validateIndexExpr(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return domain.NewError(domain.CodeValidation, "es.query", "未指定查询目标索引")
	}
	if strings.Contains(expr, "..") {
		return domain.NewError(domain.CodeValidation, "es.query", "非法的索引表达式 %q", expr)
	}
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "-"))
		if part == "" {
			continue
		}
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || strings.ContainsRune("._-+*", r) {
				continue
			}
			return domain.NewError(domain.CodeValidation, "es.query",
				"索引表达式 %q 含非法字符 %q", expr, r)
		}
	}
	return nil
}

func asMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	default:
		return nil
	}
}

func toSlice(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}

func numberFromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i
	default:
		return 0
	}
}

func truncate(s string) string {
	if len(s) <= maxCellText {
		return s
	}
	if !utf8.ValidString(s) {
		return s[:maxCellText] + "…"
	}
	return s[:maxCellText] + "…"
}

// flattenPaths 把嵌套 JSON 展平成 a.b 路径，数组保持为整体，字段数与深度均有上限。
func flattenPaths(doc map[string]any, prefix string, depth int, out *[]string, seen map[string]bool) {
	if len(*out) >= maxProjectionColumns || depth > 3 {
		return
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(*out) >= maxProjectionColumns {
			return
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if seen[path] {
			continue
		}
		if nested := asMap(doc[k]); len(nested) > 0 {
			flattenPaths(nested, path, depth+1, out, seen)
			continue
		}
		seen[path] = true
		*out = append(*out, path)
	}
}

func lookupPath(doc map[string]any, path string) (any, bool) {
	var current any = doc
	for _, p := range strings.Split(path, ".") {
		m := asMap(current)
		if m == nil {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		current = v
	}
	return current, true
}

func cellValue(v any) domain.Cell {
	if v == nil {
		return domain.NewNullCell()
	}
	switch t := v.(type) {
	case string:
		return domain.NewTextCell(truncate(t))
	case bool:
		return domain.Cell{Kind: domain.ValueNumber, Text: strconv.FormatBool(t)}
	case float64, float32, int, int8, int16, int32, int64, uint, uint32, uint64, json.Number:
		return domain.Cell{Kind: domain.ValueNumber, Text: fmt.Sprint(t)}
	default:
		if encoded, err := json.Marshal(v); err == nil {
			return domain.Cell{Kind: domain.ValueJSON, Text: truncate(string(encoded))}
		}
		return domain.NewTextCell(truncate(fmt.Sprint(v)))
	}
}

// parseScalar 把编辑/导入表单中的字符串还原为 JSON 类型，保留嵌套对象与数组。
func parseScalar(text string) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err == nil {
		switch v.(type) {
		case map[string]any, []any, float64, bool, nil:
			return v
		}
	}
	return text
}

// documentJSON 把 _search 命中转换为「_source + _id」的原始文档文本。
func documentJSON(hit map[string]any) (string, map[string]any, error) {
	doc := make(map[string]any, len(hit)+1)
	for k, v := range asMap(hit["_source"]) {
		doc[k] = v
	}
	if _, exists := doc["_id"]; !exists {
		if id, ok := hit["_id"].(string); ok {
			doc["_id"] = id
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", nil, domain.WrapError(domain.CodeQuery, "es.result", err, "文档序列化失败")
	}
	return string(encoded), doc, nil
}

// esFieldType 把通用列类型映射为 ES mapping 字段定义。
func esFieldType(sqlType string) map[string]any {
	upper := strings.ToUpper(sqlType)
	switch {
	case strings.Contains(upper, "KEYWORD"):
		return map[string]any{"type": "keyword", "ignore_above": 256}
	case strings.Contains(upper, "INT"), strings.Contains(upper, "SERIAL"):
		return map[string]any{"type": "long"}
	case strings.Contains(upper, "DECIMAL"), strings.Contains(upper, "FLOAT"),
		strings.Contains(upper, "DOUBLE"), strings.Contains(upper, "NUMBER"),
		strings.Contains(upper, "NUMERIC"):
		return map[string]any{"type": "double"}
	case strings.Contains(upper, "BOOL"):
		return map[string]any{"type": "boolean"}
	case strings.Contains(upper, "DATE"), strings.Contains(upper, "TIME"):
		return map[string]any{"type": "date"}
	case strings.Contains(upper, "BINARY"), strings.Contains(upper, "BLOB"):
		return map[string]any{"type": "binary"}
	case strings.Contains(upper, "JSON"), strings.Contains(upper, "OBJECT"), strings.Contains(upper, "ARRAY"):
		return map[string]any{"type": "object"}
	default:
		return map[string]any{
			"type":   "text",
			"fields": map[string]any{"keyword": map[string]any{"type": "keyword", "ignore_above": 256}},
		}
	}
}

// propertiesFromColumns 把结构化字段定义转成 mappings.properties。
// 含点路径的字段交给动态映射（ES 会自动展开为 object），避免写入非法字段名。
func propertiesFromColumns(columns []domain.ColumnSpec) map[string]any {
	props := make(map[string]any, len(columns))
	for _, c := range columns {
		name := strings.TrimSpace(c.Name)
		if name == "" || name == "_id" || strings.Contains(name, ".") {
			continue
		}
		props[name] = esFieldType(c.Type)
	}
	return props
}

// columnsFromMappings 把 ES mapping 递归展开为列定义（比采样推断更准确）。
func columnsFromMappings(properties map[string]any, prefix string, depth int, out *[]domain.ColumnInfo) {
	if len(*out) >= maxProjectionColumns || depth > 3 {
		return
	}
	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(*out) >= maxProjectionColumns {
			return
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		def := asMap(properties[k])
		typ, _ := def["type"].(string)
		if typ == "" {
			typ = "object"
		}
		*out = append(*out, domain.ColumnInfo{
			Position: len(*out) + 1, Name: path, DataType: typ, RawType: typ,
			Nullable: true, Comment: "mapping",
		})
		if nested := asMap(def["properties"]); len(nested) > 0 {
			columnsFromMappings(nested, path, depth+1, out)
		}
	}
}

// parseOptions 解析与方言无关的 JSON 选项文本；空文本返回空 map。
func parseOptions(text string) (map[string]any, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "es.options", err, "选项不是合法 JSON 对象")
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
