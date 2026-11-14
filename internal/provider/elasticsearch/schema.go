package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// schemaEditor 生成 Dev-Tools 风格的 Elasticsearch REST 语句（METHOD /path + JSON body），
// 而非内部 es_command JSON，使预览脚本只含真正的 ES 请求。
type schemaEditor struct {
	client *esClient
	cfg    domain.ConnectionConfig
}

func (e *schemaEditor) CreateDatabaseScript(_ context.Context, spec domain.DatabaseSpec) (domain.Script, error) {
	if err := e.validateDatabase(spec.Name); err != nil {
		return domain.Script{}, err
	}
	opts, err := parseOptions(spec.Options)
	if err != nil {
		return domain.Script{}, err
	}
	path := e.client.templatePath(spec.Name)
	create, err := encDevTools(http.MethodPut, path,
		templateBody(e.client, spec.Name, settingsFromOptions(opts)))
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encDevTools(http.MethodDelete, path, nil)
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{create}, []string{rollback}, []string{
		"Elasticsearch 没有空数据库，此处创建的是组合索引模板（dbfly_<db>），索引模式为 <db>.*。",
		"模板只影响后续新建的索引，已有索引不受影响；回滚只删除模板，不删除索引。",
	}), nil
}

func (e *schemaEditor) AlterDatabaseScript(_ context.Context, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	if err := e.validateDatabase(name); err != nil {
		return domain.Script{}, err
	}
	opts, err := parseOptions(spec.Options)
	if err != nil {
		return domain.Script{}, err
	}
	stmt, err := encDevTools(http.MethodPut, e.client.templatePath(name),
		templateBody(e.client, name, settingsFromOptions(opts)))
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{stmt}, nil, []string{
		"覆盖索引模板会立即影响之后创建的索引，已有索引的 settings/mappings 不会改变。",
	}), nil
}

func (e *schemaEditor) CreateTableScript(_ context.Context, def domain.TableDefinition) (domain.Script, error) {
	if err := validateName(def.Ref.Name, "索引名"); err != nil {
		return domain.Script{}, err
	}
	if db := strings.TrimSpace(def.Ref.Database); db != "" && db != defaultDatabase {
		if err := validateName(db, "数据库名"); err != nil {
			return domain.Script{}, err
		}
	}
	opts, err := parseOptions(def.Options)
	if err != nil {
		return domain.Script{}, err
	}
	physical := physicalIndex(e.cfg, def.Ref.Database, def.Ref.Name)
	body := createIndexBody(opts, def.Columns, e.esTypeName())
	create, err := encDevTools(http.MethodPut, "/"+physical, body)
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encDevTools(http.MethodDelete, "/"+physical, nil)
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{create}, []string{rollback}, []string{
		"字段定义会转换为 mappings.properties；含点路径的字段交给动态映射。",
		"删除索引会丢失全部文档且不可恢复；如需重建请用 reindex。",
		"索引创建成功后需要等待分片分配，写入前可先执行 _refresh。",
	}), nil
}

func (e *schemaEditor) AlterTableScript(_ context.Context, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	if err := validateName(ref.Name, "索引名"); err != nil {
		return domain.Script{}, err
	}
	statements := make([]string, 0, len(changes))
	var warnings []string
	for _, ch := range changes {
		opts, err := parseOptions(ch.Options)
		if err != nil {
			return domain.Script{}, err
		}
		var stmt string
		physical := physicalIndex(e.cfg, ref.Database, ref.Name)
		switch ch.Kind {
		case domain.ChangeAddColumn, domain.ChangeModifyColumn:
			body := opts
			if len(body) == 0 {
				name := strings.TrimSpace(ch.Column.Name)
				if name == "" {
					return domain.Script{}, domain.NewError(domain.CodeValidation, "es.script",
						"新增字段需要提供字段名或 options JSON")
				}
				body = map[string]any{"properties": map[string]any{name: esFieldType(ch.Column.Type)}}
			}
			stmt, err = encDevTools(http.MethodPut, "/"+physical+"/_mapping", body)
			if err != nil {
				return domain.Script{}, err
			}
			warnings = append(warnings, "ES mapping 只允许新增字段或新增子字段，字段类型冲突会被服务端拒绝。")
		case domain.ChangeTableOptions, domain.ChangeCollectionOpts:
			if len(opts) == 0 {
				return domain.Script{}, domain.NewError(domain.CodeValidation, "es.script",
					"修改索引设置需要提供 options JSON")
			}
			stmt, err = encDevTools(http.MethodPut, "/"+physical+"/_settings", opts)
			if err != nil {
				return domain.Script{}, err
			}
		case domain.ChangeRenameTable:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "es.script",
				"Elasticsearch 不支持重命名索引，请使用 reindex + 别名切换")
		case domain.ChangeAddIndex, domain.ChangeDropIndex:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "es.script",
				"Elasticsearch 没有独立二级索引，字段是否可检索由 mapping 决定")
		default:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "es.script",
				"Elasticsearch 不支持变更类型 %q", ch.Kind)
		}
		statements = append(statements, stmt)
	}
	return commandScript(statements, nil, warnings), nil
}

func (e *schemaEditor) DropObjectScript(_ context.Context, ref domain.ObjectRef) (domain.Script, error) {
	physical := physicalIndex(e.cfg, ref.Database, ref.Name)
	stmt, err := encDevTools(http.MethodDelete, "/"+physical, nil)
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{stmt}, nil, []string{
		"删除索引会同时删除全部文档、mapping 与 settings，不可恢复。",
	}), nil
}

func (e *schemaEditor) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	physical := physicalIndex(e.cfg, ref.Database, ref.Name)
	if err := validateIndexExpr(physical); err != nil {
		return "", err
	}
	def, err := fetchIndexDefinition(ctx, e.client, physical)
	if err != nil {
		return "", mapQueryError(err)
	}
	encoded, err := marshalIndent(def)
	if err != nil {
		return "", domain.WrapError(domain.CodeQuery, "es.script", err, "索引定义序列化失败")
	}
	return "PUT /" + physical + "\n" + encoded, nil
}

func (e *schemaEditor) Apply(ctx context.Context, script domain.Script) (domain.ApplyResult, error) {
	result := domain.ApplyResult{Outcomes: make([]domain.ExecOutcome, 0, len(script.Statements))}
	for _, text := range script.Statements {
		// 兼容三种脚本格式：
		//  1. 旧版 es_command JSON（含 "action" 键，可能带 database 占位符）；
		//  2. JSON REST（{"method","path","body"}）；
		//  3. Dev-Tools 风格（首行 METHOD /path + JSON body）。
		// 含 "action" 的 JSON 优先按 es_command 解析，以保留 database 占位符信息。
		trimmed := strings.TrimSpace(text)
		var cmd esCommand
		if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"action"`) {
			c, err := decodeCommand(text)
			if err != nil {
				return result, err
			}
			cmd = c
		} else {
			method, path, body, isREST := parseDevTools(text)
			if !isREST {
				return result, domain.NewError(domain.CodeValidation, "es.script",
					"无法识别的 Elasticsearch 脚本语句：%s", truncate(text))
			}
			cmd = esCommand{Method: method, Path: path, Body: body}
		}
		message, err := e.runCommand(ctx, cmd)
		if err != nil {
			return result, err
		}
		result.Outcomes = append(result.Outcomes, domain.ExecOutcome{
			Statement: truncate(text), Message: message,
		})
	}
	return result, nil
}

func (e *schemaEditor) runCommand(ctx context.Context, cmd esCommand) (string, error) {
	if cmd.Path != "" {
		path, err := resolvePath(cmd.Path, cmd.Database, e.cfg)
		if err != nil {
			return "", err
		}
		method := strings.ToUpper(strings.TrimSpace(cmd.Method))
		if method == "" {
			method = http.MethodPost
		}
		var body any
		if cmd.Body != nil {
			body = cmd.Body
		} else if len(cmd.Options) > 0 {
			body = cmd.Options
		}
		// 控制台直接粘贴的 ES7 风格无类型 mappings（PUT /index {mappings:{properties}}）
		// 在旧版 Elasticsearch（5.x/6.x）上会被误解析成类型名 “properties” 而报错。
		// 仅对 PUT 创建索引做类型包裹适配，其它请求（搜索/设置等）不受影响。
		if tn := e.esTypeName(); tn != "" && method == http.MethodPut {
			body = wrapMappingsType(body, tn)
		}
		var resp map[string]any
		status, err := e.client.jsonCall(ctx, method, path, body, &resp)
		if err != nil {
			return "", mapQueryError(err)
		}
		msg := fmt.Sprintf("%s %s 执行成功 (HTTP %d)", method, path, status)
		if len(resp) > 0 {
			if b, e := json.MarshalIndent(resp, "", "  "); e == nil {
				msg += "\n" + string(b)
			}
		}
		return msg, nil
	}

	switch cmd.Action {
	case "createDatabase", "alterDatabase":
		if err := e.validateDatabase(cmd.Database); err != nil {
			return "", err
		}
		path := e.client.templatePath(cmd.Database)
		if cmd.Action == "createDatabase" && cmd.IfNotExists {
			exists, err := e.client.exists(ctx, path)
			if err != nil {
				return "", mapQueryError(err)
			}
			if exists {
				return "索引模板已存在，跳过创建", nil
			}
		}
		if _, err := e.client.jsonCall(ctx, http.MethodPut, path,
			templateBody(e.client, cmd.Database, cmd.Options), nil); err != nil {
			return "", mapQueryError(err)
		}
		return cmd.Action + " 执行成功", nil

	case "dropDatabase":
		if err := validateName(cmd.Database, "数据库名"); err != nil {
			return "", err
		}
		if _, err := e.client.jsonCall(ctx, http.MethodDelete,
			e.client.templatePath(cmd.Database), nil, nil); err != nil {
			return "", mapQueryError(err)
		}
		return "dropDatabase 执行成功", nil

	case "createTable":
		physical := e.physicalOf(cmd.Database, cmd.Collection, cmd.Index)
		if err := validateName(physical, "索引名"); err != nil {
			return "", err
		}
		if cmd.IfNotExists {
			exists, err := e.client.exists(ctx, "/"+physical)
			if err != nil {
				return "", mapQueryError(err)
			}
			if exists {
				return "索引已存在，跳过创建", nil
			}
		}
		var body any = cmd.Options
		if body == nil {
			body = map[string]any{}
		}
		// 不发送 mappings.dynamic：标量 dynamic 会触发 ES 的 class_cast_exception
		// （见 ES issue #41120），Elasticsearch 默认 dynamic=true。
		if m, ok := body.(map[string]any)["mappings"].(map[string]any); ok {
			delete(m, "dynamic")
		}
		// 旧版 Elasticsearch（5.x/6.x）需要 mappings 带类型包裹（mappings.doc.properties）。
		if tn := e.esTypeName(); tn != "" {
			body = wrapMappingsType(body, tn)
		}
		if _, err := e.client.jsonCall(ctx, http.MethodPut, "/"+physical, body, nil); err != nil {
			return "", mapQueryError(err)
		}
		return "索引 " + physical + " 创建成功", nil

	case "putMapping", "putSettings":
		physical := e.physicalOf(cmd.Database, cmd.Collection, cmd.Index)
		if err := validateIndexExpr(physical); err != nil {
			return "", err
		}
		suffix := "/_mapping"
		if cmd.Action == "putSettings" {
			suffix = "/_settings"
		}
		if _, err := e.client.jsonCall(ctx, http.MethodPut,
			"/"+physical+suffix, cmd.Options, nil); err != nil {
			return "", mapQueryError(err)
		}
		return cmd.Action + " 执行成功", nil

	case "dropObject", "dropIndex":
		physical := e.physicalOf(cmd.Database, cmd.Collection, cmd.Index)
		if err := validateIndexExpr(physical); err != nil {
			return "", err
		}
		if _, err := e.client.jsonCall(ctx, http.MethodDelete, "/"+physical, nil, nil); err != nil {
			return "", mapQueryError(err)
		}
		return "索引 " + physical + " 已删除", nil

	case "refresh":
		physical := e.physicalOf(cmd.Database, cmd.Collection, cmd.Index)
		if err := validateIndexExpr(physical); err != nil {
			return "", err
		}
		if _, err := e.client.jsonCall(ctx, http.MethodPost,
			"/"+physical+"/_refresh", nil, nil); err != nil {
			return "", mapQueryError(err)
		}
		return "索引已刷新", nil

	default:
		return "", domain.NewError(domain.CodeUnsupported, "es.script",
			"不支持的 Elasticsearch 命令动作 %q", cmd.Action)
	}
}

func (e *schemaEditor) physicalOf(database, collection, index string) string {
	if strings.TrimSpace(index) != "" {
		return strings.TrimSpace(index)
	}
	return physicalIndex(e.cfg, database, collection)
}

func (e *schemaEditor) validateDatabase(name string) error {
	if name == defaultDatabase || strings.TrimSpace(name) == "" {
		return domain.NewError(domain.CodeValidation, "es.script",
			"请选择具体的数据库名，default 是内置的无命名空间分组")
	}
	if err := validateName(name, "数据库名"); err != nil {
		return err
	}
	if namespaceSeparator(e.cfg) == "" {
		return domain.NewError(domain.CodeValidation, "es.script",
			"连接参数 namespaceSeparator 为空时不支持创建逻辑库（索引模板）")
	}
	return nil
}

func resolvePath(path, database string, cfg domain.ConnectionConfig) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.Contains(path, "{index}") || strings.Contains(path, "{database}") {
		if strings.TrimSpace(database) == "" {
			return "", domain.NewError(domain.CodeValidation, "es.script",
				"路径含 {index}/{database} 占位符，需要指定数据库")
		}
		path = strings.ReplaceAll(path, "{index}", physicalIndex(cfg, "", database))
		path = strings.ReplaceAll(path, "{database}", database)
	}
	return path, nil
}

// settingsFromOptions 兼容扁平写法与 {"settings": {...}} 嵌套写法。
func settingsFromOptions(opts map[string]any) map[string]any {
	settings := map[string]any{}
	if nested := asMap(opts["settings"]); len(nested) > 0 {
		for k, v := range nested {
			settings[k] = v
		}
	}
	for _, key := range []string{
		"number_of_shards", "number_of_replicas", "refresh_interval", "analysis",
		"max_result_window", "default_pipeline", "final_pipeline",
	} {
		if v, ok := opts[key]; ok {
			settings[key] = v
		}
	}
	return settings
}

// esTypeName 返回创建索引时需要的「类型名」：
//   - ES 7+ / OpenSearch：返回空串，mappings.properties 直接写法即可（无类型）；
//   - ES 5.x / 6.x：返回 "doc"，需要把字段包进 mappings.doc.properties。
//
// 注意：ES 5.x 的 type 名不能以 '_' 开头，所以不能用 '_doc'（ES 6 才把 _doc 设为保留类型），
// 统一用 "doc" 兼容 5.x 与 6.x。
// 无连接（如单元测试）时按 ES 7 处理（无类型）。
func (e *schemaEditor) esTypeName() string {
	if e.client == nil {
		return ""
	}
	if e.client.isOpenSearch() {
		return ""
	}
	if e.client.majorVersion() < 7 {
		return "doc"
	}
	return ""
}

// wrapMappingsType 将 ES7 风格的无类型 mappings（mappings.properties）转换为
// 旧版 Elasticsearch（5.x/6.x）必须的「类型包裹」结构（mappings.<typeName>.properties）。
// 仅当 body 顶层 mappings 直接含 properties 时生效；已是类型包裹结构
// （mappings.<type>.properties）的保持原样，避免二次包裹。typeName 为空时原样返回。
func wrapMappingsType(body any, typeName string) any {
	if typeName == "" {
		return body
	}
	m, ok := body.(map[string]any)
	if !ok {
		return body
	}
	mappings, ok := m["mappings"].(map[string]any)
	if !ok {
		return body
	}
	if _, hasProps := mappings["properties"]; !hasProps {
		return body
	}
	wrapped := map[string]any{}
	for k, v := range mappings {
		wrapped[k] = v
	}
	m["mappings"] = map[string]any{typeName: wrapped}
	return body
}

func createIndexBody(opts map[string]any, columns []domain.ColumnSpec, typeName ...string) map[string]any {
	tn := ""
	if len(typeName) > 0 {
		tn = typeName[0]
	}
	body := map[string]any{}
	settings := settingsFromOptions(opts)
	if len(settings) == 0 {
		settings = map[string]any{"number_of_shards": 1, "number_of_replicas": 1}
	}
	body["settings"] = settings

	mappings := asMap(opts["mappings"])
	// 不发送 mappings.dynamic：部分 Elasticsearch 版本 / 代理在解析无类型 mappings 时，
	// 会把顶层标量 dynamic 当成类型名并尝试转为 Map，从而抛出
	// "Boolean/String cannot be cast to java.util.Map"（见 ES issue #41120）。
	// Elasticsearch 默认 dynamic=true，与默认行为一致，故省略即可。
	delete(mappings, "dynamic")

	if len(mappings) == 0 {
		mappings = map[string]any{}
		if props := propertiesFromColumns(columns); len(props) > 0 {
			mappings["properties"] = props
		}
	} else if props := asMap(mappings["properties"]); len(props) > 0 {
		// 标准写法：已显式包裹 properties，仅把 "field": "text" 这类字符串值规范化为 {"type":"text"}，
		// 否则 Elasticsearch 会报 class_cast_exception: String cannot be cast to java.util.Map。
		mappings["properties"] = normalizeProperties(props)
	} else if props := inferFieldDefs(mappings); len(props) > 0 {
		// 高级 mappings 直接写字段定义（无 properties 包裹）时，自动包进 properties，
		// 否则 Elasticsearch 报 "Root mapping definition has unsupported parameters: [name : ...]"。
		rest := map[string]any{}
		for k, v := range mappings {
			if !isFieldDef(v) {
				rest[k] = v
			}
		}
		mappings = map[string]any{"properties": normalizeProperties(props)}
		for k, v := range rest {
			mappings[k] = v
		}
	}
	if len(mappings) > 0 {
		body["mappings"] = mappings
	}

	// Elasticsearch 5.x/6.x 的 mappings 必须是「类型包裹」结构：mappings.<type>.properties。
	// 7+ 与 OpenSearch 支持无类型的 mappings.properties 直接写法。typeName 为空表示无需包裹。
	if tn != "" {
		if m, ok := body["mappings"].(map[string]any); ok {
			body["mappings"] = map[string]any{tn: m}
		}
	}

	if aliases := normalizeAliases(opts["aliases"]); len(aliases) > 0 {
		body["aliases"] = aliases
	}
	return body
}

// normalizeProperties 把属性表规范化为 ES 接受的形式：把 "field": "text" 这类字符串值
// 转为 {"type":"text"}，否则 ES 会报 class_cast_exception: String cannot be cast to java.util.Map。
func normalizeProperties(props map[string]any) map[string]any {
	normalized := make(map[string]any, len(props))
	for name, def := range props {
		if s, ok := def.(string); ok {
			normalized[name] = map[string]any{"type": s}
		} else {
			normalized[name] = def
		}
	}
	return normalized
}

// inferFieldDefs 从 mappings 顶层挑出“字段定义”条目（含 type/properties/fields 的映射），
// 用于把“直接写字段、未包 properties”的高级 mappings 自动包进 properties。
func inferFieldDefs(m map[string]any) map[string]any {
	props := make(map[string]any, len(m))
	for k, v := range m {
		if isFieldDef(v) {
			props[k] = v
		}
	}
	return props
}

// isFieldDef 判断一个值是否像 ES 字段定义：可以是 "text" 这类字符串简写，
// 或含 type/properties/fields 的对象。
func isFieldDef(v any) bool {
	switch t := v.(type) {
	case string:
		return true
	case map[string]any:
		if _, has := t["type"]; has {
			return true
		}
		if _, has := t["properties"]; has {
			return true
		}
		if _, has := t["fields"]; has {
			return true
		}
		return false
	default:
		return false
	}
}

// normalizeAliases 把别名定义规范化为对象：ES 8.x 已移除字符串形式的别名，
// 必须为 {"aliasName": {}} 结构，否则报 class_cast_exception。
func normalizeAliases(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case string:
		if s := strings.TrimSpace(t); s != "" {
			return map[string]any{s: map[string]any{}}
		}
	case []any:
		out := make(map[string]any, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out[s] = map[string]any{}
				}
			}
		}
		return out
	}
	return nil
}

func templateBody(c *esClient, db string, opts map[string]any) map[string]any {
	settings := settingsFromOptions(opts)
	mappings := asMap(opts["mappings"])
	aliases := asMap(opts["aliases"])
	pattern := db + namespaceSeparator(c.cfg) + "*"
	out := map[string]any{
		"index_patterns": []any{pattern},
		"priority":       100,
		"_meta":          map[string]any{"managed_by": userAgent, "database": db},
	}
	if c.composableTemplates {
		// 组合模板要求 template 字段存在且非空（至少含 settings），
		// 否则 Elasticsearch 报 "template is missing"。
		template := map[string]any{}
		if len(settings) > 0 {
			template["settings"] = settings
		} else {
			template["settings"] = map[string]any{}
		}
		if len(mappings) > 0 {
			template["mappings"] = mappings
		}
		if len(aliases) > 0 {
			template["aliases"] = aliases
		}
		out["template"] = template
		return out
	}
	// 7.7 及以前使用 legacy template：settings/mappings 位于顶层，
	// 且必须提供（可为空的）组件模板列表，否则同样报 "template is missing"。
	if len(settings) > 0 {
		out["settings"] = settings
	}
	if len(mappings) > 0 {
		out["mappings"] = mappings
	}
	if len(aliases) > 0 {
		out["aliases"] = aliases
	}
	out["order"] = 100
	out["template"] = []any{}
	return out
}
