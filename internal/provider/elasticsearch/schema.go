package elasticsearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// schemaEditor 生成 es_command JSON 脚本（REST 语义由本模块解释），而非 SQL。
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
	create, err := encodeCommand(esCommand{
		Action: "createDatabase", Database: spec.Name,
		Options: settingsFromOptions(opts), IfNotExists: spec.IfNotExists,
	})
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encodeCommand(esCommand{Action: "dropDatabase", Database: spec.Name})
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
	stmt, err := encodeCommand(esCommand{
		Action: "alterDatabase", Database: name, Options: settingsFromOptions(opts),
	})
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
	body := createIndexBody(opts, def.Columns)
	create, err := encodeCommand(esCommand{
		Action: "createTable", Database: def.Ref.Database, Collection: def.Ref.Name,
		Options: body, IfNotExists: def.IfNotExists,
	})
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encodeCommand(esCommand{
		Action: "dropObject", Database: def.Ref.Database, Collection: def.Ref.Name,
	})
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
		var cmd esCommand
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
			cmd = esCommand{
				Action: "putMapping", Database: ref.Database, Collection: ref.Name, Options: body,
			}
			warnings = append(warnings, "ES mapping 只允许新增字段或新增子字段，字段类型冲突会被服务端拒绝。")
		case domain.ChangeTableOptions, domain.ChangeCollectionOpts:
			if len(opts) == 0 {
				return domain.Script{}, domain.NewError(domain.CodeValidation, "es.script",
					"修改索引设置需要提供 options JSON")
			}
			cmd = esCommand{
				Action: "putSettings", Database: ref.Database, Collection: ref.Name, Options: opts,
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
		stmt, err := encodeCommand(cmd)
		if err != nil {
			return domain.Script{}, err
		}
		statements = append(statements, stmt)
	}
	return commandScript(statements, nil, warnings), nil
}

func (e *schemaEditor) DropObjectScript(_ context.Context, ref domain.ObjectRef) (domain.Script, error) {
	stmt, err := encodeCommand(esCommand{
		Action: "dropObject", Database: ref.Database, Collection: ref.Name,
	})
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
		cmd, err := decodeCommand(text)
		if err != nil {
			return result, err
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
		status, err := e.client.jsonCall(ctx, method, path, body, nil)
		if err != nil {
			return "", mapQueryError(err)
		}
		return fmt.Sprintf("%s %s 执行成功 (HTTP %d)", method, path, status), nil
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
		body := cmd.Options
		if body == nil {
			body = map[string]any{}
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

func createIndexBody(opts map[string]any, columns []domain.ColumnSpec) map[string]any {
	body := map[string]any{}
	settings := settingsFromOptions(opts)
	if len(settings) == 0 {
		settings = map[string]any{"number_of_shards": 1, "number_of_replicas": 1}
	}
	body["settings"] = settings
	if mappings := asMap(opts["mappings"]); len(mappings) > 0 {
		body["mappings"] = mappings
	} else {
		mappings := map[string]any{}
		if dynamic, ok := opts["dynamic"]; ok {
			mappings["dynamic"] = dynamic
		}
		if props := propertiesFromColumns(columns); len(props) > 0 {
			mappings["properties"] = props
		}
		if len(mappings) > 0 {
			body["mappings"] = mappings
		}
	}
	if aliases := asMap(opts["aliases"]); len(aliases) > 0 {
		body["aliases"] = aliases
	}
	return body
}

func templateBody(c *esClient, db string, opts map[string]any) map[string]any {
	settings := settingsFromOptions(opts)
	mappings := asMap(opts["mappings"])
	aliases := asMap(opts["aliases"])
	pattern := db + namespaceSeparator(c.cfg) + "*"
	template := map[string]any{}
	if len(settings) > 0 {
		template["settings"] = settings
	}
	if len(mappings) > 0 {
		template["mappings"] = mappings
	}
	if len(aliases) > 0 {
		template["aliases"] = aliases
	}
	out := map[string]any{
		"index_patterns": []any{pattern},
		"priority":       100,
		"_meta":          map[string]any{"managed_by": userAgent, "database": db},
	}
	if c.composableTemplates {
		out["template"] = template
		return out
	}
	// 7.7 及以前使用 legacy template：settings/mappings 位于顶层。
	for k, v := range template {
		out[k] = v
	}
	out["order"] = 100
	return out
}
