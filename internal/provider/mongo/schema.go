package mongo

import (
	"context"
	"encoding/json"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/db-fly/db-fly/internal/domain"
)

// schemaEditor 生成 MongoDB 命令式脚本（JSON），而非 SQL。
// 这是业务隔离的关键：Script 的语义由各 provider 自行定义。
type schemaEditor struct {
	client *mongo.Client
}

func validateName(name, what string) error {
	if strings.TrimSpace(name) == "" {
		return domain.NewError(domain.CodeValidation, "mongo.script", "%s不能为空", what)
	}
	if len(name) > 120 || strings.ContainsAny(name, "/\\.\"$*<>:|?\x00") {
		return domain.NewError(domain.CodeValidation, "mongo.script", "非法的%s: %q", what, name)
	}
	return nil
}

func commandScript(statements []string, rollbacks, warnings []string) domain.Script {
	return domain.Script{
		Format: "mongo_command", Statements: statements,
		Rollback: rollbacks, Warnings: warnings,
	}
}

func (e *schemaEditor) CreateDatabaseScript(ctx context.Context, spec domain.DatabaseSpec) (domain.Script, error) {
	if err := validateName(spec.Name, "数据库名"); err != nil {
		return domain.Script{}, err
	}
	stmt, err := encodeCommand(mongoCommand{
		Action: "createDatabase", Database: spec.Name, Collection: "_dbfly_init",
	})
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encodeCommand(mongoCommand{Action: "dropDatabase", Database: spec.Name})
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{stmt}, []string{rollback}, []string{
		"MongoDB 没有独立的建库命令，通过创建首个集合 _dbfly_init 使数据库落盘。",
		"回滚会删除整个数据库及其全部集合。",
	}), nil
}

func (e *schemaEditor) AlterDatabaseScript(ctx context.Context, name string, spec domain.DatabaseSpec) (domain.Script, error) {
	return domain.Script{}, domain.NewError(domain.CodeUnsupported, "mongo.script",
		"MongoDB 不支持修改数据库属性；请通过集合选项或索引管理结构")
}

// jsonSchemaFromColumns 把结构化字段定义转成 $jsonSchema 校验器。
func jsonSchemaFromColumns(columns []domain.ColumnSpec) map[string]any {
	properties := make(map[string]any, len(columns))
	required := make([]any, 0, len(columns))
	for _, c := range columns {
		properties[c.Name] = map[string]any{"bsonType": mongoBSONType(c.Type), "description": c.Comment}
		if !c.Nullable {
			required = append(required, c.Name)
		}
	}
	schema := map[string]any{
		"bsonType":   "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return map[string]any{"$jsonSchema": schema}
}

func mongoBSONType(sqlType string) string {
	upper := strings.ToUpper(sqlType)
	switch {
	case strings.Contains(upper, "INT"), strings.Contains(upper, "DECIMAL"), strings.Contains(upper, "FLOAT"), strings.Contains(upper, "DOUBLE"), strings.Contains(upper, "NUMBER"):
		return "number"
	case strings.Contains(upper, "BOOL"):
		return "bool"
	case strings.Contains(upper, "DATE"), strings.Contains(upper, "TIME"):
		return "date"
	case strings.Contains(upper, "JSON"), strings.Contains(upper, "OBJECT"), strings.Contains(upper, "ARRAY"):
		return "object"
	default:
		return "string"
	}
}

func (e *schemaEditor) CreateTableScript(ctx context.Context, def domain.TableDefinition) (domain.Script, error) {
	if err := validateName(def.Ref.Database, "数据库名"); err != nil {
		return domain.Script{}, err
	}
	if err := validateName(def.Ref.Name, "集合名"); err != nil {
		return domain.Script{}, err
	}
	statements := make([]string, 0, 1+len(def.Indexes))
	create := mongoCommand{
		Action: "createCollection", Database: def.Ref.Database, Collection: def.Ref.Name,
	}
	if len(def.Columns) > 0 {
		create.Validator = jsonSchemaFromColumns(def.Columns)
	}
	stmt, err := encodeCommand(create)
	if err != nil {
		return domain.Script{}, err
	}
	statements = append(statements, stmt)
	for _, idx := range def.Indexes {
		keys := make(map[string]int, len(idx.Columns))
		for _, c := range idx.Columns {
			keys[c] = 1
		}
		indexStmt, err := encodeCommand(mongoCommand{
			Action: "createIndex", Database: def.Ref.Database, Collection: def.Ref.Name,
			IndexName: idx.Name, Keys: keys, Unique: idx.Unique,
		})
		if err != nil {
			return domain.Script{}, err
		}
		statements = append(statements, indexStmt)
	}
	rollback, err := encodeCommand(mongoCommand{
		Action: "dropCollection", Database: def.Ref.Database, Collection: def.Ref.Name,
	})
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript(statements, []string{rollback}, []string{
		"MongoDB 无固定 schema；字段定义通过 $jsonSchema 校验器表达，仅约束后续写入。",
		"若提供 Engine/Charset/Collation 将被忽略。",
	}), nil
}

func (e *schemaEditor) AlterTableScript(ctx context.Context, ref domain.ObjectRef, changes []domain.SchemaChange) (domain.Script, error) {
	if err := validateName(ref.Database, "数据库名"); err != nil {
		return domain.Script{}, err
	}
	if err := validateName(ref.Name, "集合名"); err != nil {
		return domain.Script{}, err
	}
	if len(changes) == 1 && changes[0].Kind == domain.ChangeRenameTable {
		return e.renameCollectionScript(ref, changes[0].NewName)
	}
	statements := make([]string, 0, len(changes))
	rollbacks := make([]string, 0, len(changes))
	var warnings []string

	for _, ch := range changes {
		var cmd mongoCommand
		switch ch.Kind {
		case domain.ChangeCollectionOpts, domain.ChangeAddColumn, domain.ChangeModifyColumn:
			if strings.TrimSpace(ch.Validator) == "" {
				return domain.Script{}, domain.NewError(domain.CodeValidation, "mongo.script",
					"MongoDB 的集合变更需要提供完整的 validator（JSON Schema）")
			}
			var validator map[string]any
			if err := json.Unmarshal([]byte(ch.Validator), &validator); err != nil {
				return domain.Script{}, domain.WrapError(domain.CodeValidation, "mongo.script", err,
					"validator 不是合法 JSON 对象")
			}
			cmd = mongoCommand{
				Action: "alterCollection", Database: ref.Database, Collection: ref.Name,
				Validator: validator,
			}
			warnings = append(warnings, "collMod 的 validator 会整体替换，请提供完整定义而非增量。")

		case domain.ChangeAddIndex:
			keys := make(map[string]int, len(ch.Index.Columns))
			for _, c := range ch.Index.Columns {
				keys[c] = 1
			}
			cmd = mongoCommand{
				Action: "createIndex", Database: ref.Database, Collection: ref.Name,
				IndexName: ch.Index.Name, Keys: keys, Unique: ch.Index.Unique,
			}
			rollback, err := encodeCommand(mongoCommand{
				Action: "dropIndex", Database: ref.Database, Collection: ref.Name,
				IndexName: ch.Index.Name,
			})
			if err == nil {
				rollbacks = append(rollbacks, rollback)
			}

		case domain.ChangeDropIndex:
			cmd = mongoCommand{
				Action: "dropIndex", Database: ref.Database, Collection: ref.Name,
				IndexName: ch.IndexName,
			}
			warnings = append(warnings, "删除索引需人工恢复其定义。")

		case domain.ChangeRenameTable:
			return domain.Script{}, domain.NewError(domain.CodeValidation, "mongo.script",
				"重命名集合必须单独提交，不能与其他集合变更合并")

		default:
			return domain.Script{}, domain.NewError(domain.CodeUnsupported, "mongo.script",
				"MongoDB 不支持变更类型 %q", ch.Kind)
		}
		stmt, err := encodeCommand(cmd)
		if err != nil {
			return domain.Script{}, err
		}
		statements = append(statements, stmt)
	}
	return commandScript(statements, rollbacks, warnings), nil
}

// renameCollectionScript 生成 renameCollection 命令与改回原名的回滚命令。
func (e *schemaEditor) renameCollectionScript(ref domain.ObjectRef, newName string) (domain.Script, error) {
	if err := validateName(newName, "新集合名"); err != nil {
		return domain.Script{}, err
	}
	rename, err := encodeCommand(mongoCommand{
		Action: "renameCollection", Database: ref.Database,
		Collection: ref.Name, NewName: newName,
	})
	if err != nil {
		return domain.Script{}, err
	}
	rollback, err := encodeCommand(mongoCommand{
		Action: "renameCollection", Database: ref.Database,
		Collection: newName, NewName: ref.Name,
	})
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{rename}, []string{rollback}, []string{
		"renameCollection 需要 admin 数据库的权限，且目标集合不能已存在。",
		"重命名集合不会自动更新应用代码中的集合名，请人工复核。",
	}), nil
}

func (e *schemaEditor) DropObjectScript(ctx context.Context, ref domain.ObjectRef) (domain.Script, error) {
	stmt, err := encodeCommand(mongoCommand{
		Action: "dropCollection", Database: ref.Database, Collection: ref.Name,
	})
	if err != nil {
		return domain.Script{}, err
	}
	return commandScript([]string{stmt}, nil, []string{"删除集合不可恢复。"}), nil
}

// ShowCreate 返回等价的 createCollection 命令文本。
func (e *schemaEditor) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	spec, err := collectionSpec(ctx, e.client.Database(ref.Database), ref.Name)
	if err != nil {
		return "", err
	}
	doc := map[string]any{"createCollection": ref.Name}
	for k, v := range spec {
		doc[k] = v
	}
	encoded, err := bson.MarshalExtJSON(doc, true, false)
	if err != nil {
		return "", domain.WrapError(domain.CodeQuery, "mongo.script", err, "集合选项序列化失败")
	}
	return string(encoded), nil
}

// Apply 逐条解析并执行 Mongo 命令。
func (e *schemaEditor) Apply(ctx context.Context, script domain.Script) (domain.ApplyResult, error) {
	result := domain.ApplyResult{Outcomes: make([]domain.ExecOutcome, 0, len(script.Statements))}
	for _, text := range script.Statements {
		cmd, err := decodeCommand(text)
		if err != nil {
			return result, err
		}
		if err := e.runCommand(ctx, cmd); err != nil {
			return result, err
		}
		result.Outcomes = append(result.Outcomes, domain.ExecOutcome{
			Statement: truncate(text), Message: cmd.Action + " 执行成功",
		})
	}
	return result, nil
}

func (e *schemaEditor) runCommand(ctx context.Context, cmd mongoCommand) error {
	if err := validateName(cmd.Database, "数据库名"); err != nil {
		return err
	}
	db := e.client.Database(cmd.Database)

	switch cmd.Action {
	case "createDatabase":
		name := cmd.Collection
		if name == "" {
			name = "_dbfly_init"
		}
		return mapQueryError(db.CreateCollection(ctx, name))
	case "dropDatabase":
		return mapQueryError(db.Drop(ctx))
	case "createCollection":
		if err := validateName(cmd.Collection, "集合名"); err != nil {
			return err
		}
		command := bson.D{{Key: "create", Value: cmd.Collection}}
		if len(cmd.Validator) > 0 {
			command = append(command, bson.E{Key: "validator", Value: cmd.Validator})
		}
		return mapQueryError(db.RunCommand(ctx, command).Err())
	case "alterCollection":
		if len(cmd.Validator) == 0 {
			return domain.NewError(domain.CodeValidation, "mongo.script", "collMod 缺少 validator")
		}
		command := bson.D{
			{Key: "collMod", Value: cmd.Collection},
			{Key: "validator", Value: cmd.Validator},
			{Key: "validationLevel", Value: "moderate"},
		}
		return mapQueryError(db.RunCommand(ctx, command).Err())
	case "createIndex":
		keyDoc := bson.D{}
		keys := sortedIndexKeys(cmd.Keys)
		for _, k := range keys {
			keyDoc = append(keyDoc, bson.E{Key: k, Value: cmd.Keys[k]})
		}
		index := bson.D{
			{Key: "key", Value: keyDoc},
			{Key: "name", Value: cmd.IndexName},
		}
		if cmd.Unique {
			index = append(index, bson.E{Key: "unique", Value: true})
		}
		command := bson.D{
			{Key: "createIndexes", Value: cmd.Collection},
			{Key: "indexes", Value: bson.A{index}},
		}
		return mapQueryError(db.RunCommand(ctx, command).Err())
	case "dropIndex":
		return mapQueryError(db.Collection(cmd.Collection).Indexes().DropOne(ctx, cmd.IndexName))
	case "dropCollection":
		return mapQueryError(db.Collection(cmd.Collection).Drop(ctx))
	case "renameCollection":
		if err := validateName(cmd.Collection, "集合名"); err != nil {
			return err
		}
		if err := validateName(cmd.NewName, "新集合名"); err != nil {
			return err
		}
		command := bson.D{
			{Key: "renameCollection", Value: cmd.Database + "." + cmd.Collection},
			{Key: "to", Value: cmd.Database + "." + cmd.NewName},
			{Key: "dropTarget", Value: false},
		}
		return mapQueryError(e.client.Database("admin").RunCommand(ctx, command).Err())
	case "insertDocuments":
		if len(cmd.Documents) == 0 {
			return nil
		}
		docs := make([]any, 0, len(cmd.Documents))
		for _, d := range cmd.Documents {
			docs = append(docs, d)
		}
		_, err := db.Collection(cmd.Collection).InsertMany(ctx, docs)
		return mapQueryError(err)
	default:
		return domain.NewError(domain.CodeUnsupported, "mongo.script",
			"不支持的 MongoDB 命令动作 %q", cmd.Action)
	}
}

func sortedIndexKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 保持字典序，复合索引顺序由用户显式传入的 JSON 决定时可再扩展。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
