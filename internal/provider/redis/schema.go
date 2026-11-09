package redis

import (
	"context"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

const scriptFormatRedis = "redis_command"

// schemaEditor 生成命令式脚本（Format=redis_command），不是 SQL。
type schemaEditor struct {
	session *session
}

func redisScript(database string, statements, rollback, warnings []string) domain.Script {
	return domain.Script{
		Format: scriptFormatRedis, Statements: statements,
		Rollback: rollback, Warnings: warnings, Database: database,
	}
}

func (e *schemaEditor) CreateDatabaseScript(context.Context, domain.DatabaseSpec) (domain.Script, error) {
	return domain.Script{}, domain.NewError(domain.CodeUnsupported, "redis.script",
		"Redis 逻辑库数量由服务端配置决定，请修改 redis.conf 的 databases")
}

func (e *schemaEditor) AlterDatabaseScript(context.Context, string, domain.DatabaseSpec) (domain.Script, error) {
	return domain.Script{}, domain.NewError(domain.CodeUnsupported, "redis.script",
		"Redis 不支持修改逻辑库属性")
}

// CreateTableScript 在 Redis 中的语义是「创建一个键」。
func (e *schemaEditor) CreateTableScript(_ context.Context, def domain.TableDefinition) (domain.Script, error) {
	key := strings.TrimSpace(def.Ref.Name)
	if key == "" {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "redis.script", "键名不能为空")
	}
	initial := ""
	if len(def.Columns) > 0 && def.Columns[0].Default != nil {
		initial = *def.Columns[0].Default
	}
	quoted := strconv.Quote(key)
	stmt := "SET " + quoted + " " + strconv.Quote(initial)
	return redisScript(def.Ref.Database, []string{stmt}, []string{"DEL " + quoted}, []string{
		"Redis 没有表结构；字段定义、索引与表选项均被忽略。",
		"回滚脚本会直接删除该键。",
	}), nil
}

func (e *schemaEditor) AlterTableScript(context.Context, domain.ObjectRef, []domain.SchemaChange) (domain.Script, error) {
	return domain.Script{}, domain.NewError(domain.CodeUnsupported, "redis.script",
		"Redis 无固定表结构；请在查询页使用原生命令修改键值")
}

func (e *schemaEditor) DropObjectScript(_ context.Context, ref domain.ObjectRef) (domain.Script, error) {
	key := strings.TrimSpace(ref.Name)
	if key == "" {
		return domain.Script{}, domain.NewError(domain.CodeValidation, "redis.script", "键名不能为空")
	}
	return redisScript(ref.Database, []string{"DEL " + strconv.Quote(key)}, nil, []string{
		"删除键不可恢复。",
	}), nil
}

// ShowCreate 返回可复现当前键值的等价创建命令（用于「查看建表 SQL」入口）。
func (e *schemaEditor) ShowCreate(ctx context.Context, ref domain.ObjectRef) (string, error) {
	if strings.TrimSpace(ref.Name) == "" {
		return "", domain.NewError(domain.CodeValidation, "redis.script", "键名不能为空")
	}
	client, err := e.session.clientFor(ref.Database)
	if err != nil {
		return "", err
	}
	typ, err := client.Type(ctx, ref.Name).Result()
	if err != nil {
		return "", mapQueryError(err)
	}
	value := readKeyPreview(ctx, client, typ, ref.Name, 20)
	return "# Redis 键没有建表语句，以下为等价创建命令\nSET " +
		strconv.Quote(ref.Name) + " " + strconv.Quote(value), nil
}

// Apply 逐条解析并执行命令式脚本。
func (e *schemaEditor) Apply(ctx context.Context, script domain.Script) (domain.ApplyResult, error) {
	result := domain.ApplyResult{Outcomes: make([]domain.ExecOutcome, 0, len(script.Statements))}
	client, err := e.session.clientFor(script.Database)
	if err != nil {
		return result, err
	}
	for _, statement := range script.Statements {
		args, err := splitCommand(statement)
		if err != nil {
			return result, err
		}
		if _, err := client.Do(ctx, args...).Result(); err != nil {
			return result, mapQueryError(err)
		}
		result.Outcomes = append(result.Outcomes, domain.ExecOutcome{
			Statement: statement, Message: "命令执行成功",
		})
	}
	return result, nil
}

// dataEditor Redis 不走结果集单元格回写，统一通过命令脚本完成。
type dataEditor struct{}

func (dataEditor) Preview(context.Context, domain.EditRequest) ([]string, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "redis.edit",
		"Redis 不支持结果集单元格回写，请使用原生 SET / HSET / DEL 命令")
}

func (dataEditor) Apply(context.Context, domain.EditRequest) (domain.EditResult, error) {
	return domain.EditResult{}, domain.NewError(domain.CodeUnsupported, "redis.edit",
		"Redis 不支持结果集单元格回写，请使用原生 SET / HSET / DEL 命令")
}
