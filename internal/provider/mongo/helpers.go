package mongo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/db-fly/db-fly/internal/domain"
)

const (
	maxProjectionColumns = 40
	sampleDocuments      = 50
	maxCellText          = 240
)

// mongoCommand 是 Mongo 脚本中每条 JSON 语句的结构。
// Script.Format = "mongo_command" 时，Statements 为它的 JSON 文本。
type mongoCommand struct {
	Action     string           `json:"action"`
	Database   string           `json:"database"`
	Collection string           `json:"collection,omitempty"`
	NewName    string           `json:"new_name,omitempty"`
	Validator  map[string]any   `json:"validator,omitempty"`
	IndexName  string           `json:"index_name,omitempty"`
	Keys       map[string]int   `json:"keys,omitempty"`
	Unique     bool             `json:"unique,omitempty"`
	Documents  []map[string]any `json:"documents,omitempty"`
}

func encodeCommand(cmd mongoCommand) (string, error) {
	b, err := json.MarshalIndent(cmd, "", "  ")
	return string(b), err
}

func decodeCommand(text string) (mongoCommand, error) {
	var cmd mongoCommand
	if err := json.Unmarshal([]byte(text), &cmd); err != nil {
		return cmd, domain.WrapError(domain.CodeValidation, "mongo.command", err, "命令 JSON 无法解析")
	}
	if cmd.Action == "" {
		return cmd, domain.NewError(domain.CodeValidation, "mongo.command", "命令缺少 action 字段")
	}
	return cmd, nil
}

// parseExtJSON 把用户输入的 Extended JSON / 普通 JSON 解析为 bson.M。
func parseExtJSON(text string) (bson.M, error) {
	var doc bson.M
	if err := bson.UnmarshalExtJSON([]byte(text), false, &doc); err == nil {
		return doc, nil
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(text), &generic); err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "mongo.query", err,
			"查询必须是合法 JSON（支持 MongoDB Extended JSON）")
	}
	return bson.M(generic), nil
}

// parseJSONArray 解析聚合管道等 JSON 数组。
func parseJSONArray(text string) ([]any, error) {
	var out []any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, domain.WrapError(domain.CodeValidation, "mongo.query", err, "管道必须是 JSON 数组")
	}
	return out, nil
}

func rawToDocument(raw bson.Raw) (string, bson.M, error) {
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return "", nil, domain.WrapError(domain.CodeQuery, "mongo.result", err, "BSON 文档解码失败")
	}
	encoded, err := bson.MarshalExtJSON(doc, true, false)
	if err != nil {
		return "", nil, domain.WrapError(domain.CodeQuery, "mongo.result", err, "BSON 文档序列化失败")
	}
	return string(encoded), doc, nil
}

func asMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case bson.M:
		return map[string]any(t), true
	case map[string]any:
		return t, true
	case bson.D:
		out := make(map[string]any, len(t))
		for _, e := range t {
			out[e.Key] = e.Value
		}
		return out, true
	default:
		return nil, false
	}
}

// flattenPaths 把嵌套文档展平成 a.b 形式的字段路径，数组保持为整体。
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
		if nested, ok := asMap(doc[k]); ok && len(nested) > 0 {
			flattenPaths(nested, path, depth+1, out, seen)
			continue
		}
		seen[path] = true
		*out = append(*out, path)
	}
}

func lookupPath(doc map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var current any = doc
	for _, p := range parts {
		m, ok := asMap(current)
		if !ok {
			return nil, false
		}
		current, ok = m[p]
		if !ok {
			return nil, false
		}
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
		return domain.Cell{Kind: domain.ValueNumber, Text: fmt.Sprintf("%v", t)}
	case int, int8, int16, int32, int64, uint, uint32, uint64, float32, float64:
		return domain.Cell{Kind: domain.ValueNumber, Text: fmt.Sprint(t)}
	case bson.ObjectID:
		return domain.NewTextCell(t.Hex())
	case time.Time:
		return domain.NewTextCell(t.UTC().Format(time.RFC3339Nano))
	case []byte:
		return domain.NewBinaryCell(t)
	default:
		if encoded, err := bson.MarshalExtJSON(v, true, false); err == nil {
			return domain.Cell{Kind: domain.ValueJSON, Text: truncate(string(encoded))}
		}
		return domain.NewTextCell(truncate(fmt.Sprint(v)))
	}
}

func truncate(s string) string {
	if len(s) <= maxCellText {
		return s
	}
	return s[:maxCellText] + "…"
}

// numberAsInt64 兼容 JSON 与 BSON 解码后的各种数值类型。
func numberAsInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

// parseScalar 把编辑表单中的字符串值还原为合适的 BSON 值。
func parseScalar(text string) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "$oid") {
		var wrapper struct {
			OID string `json:"$oid"`
		}
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err == nil {
			if oid, err := bson.ObjectIDFromHex(wrapper.OID); err == nil {
				return oid
			}
		}
	}
	var generic any
	if err := json.Unmarshal([]byte(trimmed), &generic); err == nil {
		switch generic.(type) {
		case map[string]any, []any:
			return generic
		case float64, bool, nil:
			return generic
		}
	}
	return text
}

// resolveKeyValue 把主键文本还原为 BSON 值，_id 优先按 ObjectID 处理。
func resolveKeyValue(column string, value *string) any {
	if value == nil {
		return nil
	}
	text := strings.TrimSpace(*value)
	if column == "_id" {
		if oid, err := bson.ObjectIDFromHex(text); err == nil {
			return oid
		}
		if strings.HasPrefix(text, "{") {
			var wrapper struct {
				OID string `json:"$oid"`
			}
			if err := json.Unmarshal([]byte(text), &wrapper); err == nil {
				if oid, err := bson.ObjectIDFromHex(wrapper.OID); err == nil {
					return oid
				}
			}
		}
	}
	return parseScalar(text)
}
