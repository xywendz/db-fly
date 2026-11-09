package redis

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type queryExecutor struct {
	session *session
}

func (e *queryExecutor) Execute(ctx context.Context, q domain.Query) (domain.ResultSet, error) {
	if q.Language != "" && q.Language != domain.LangRedis {
		return nil, domain.NewError(domain.CodeUnsupported, "redis.query",
			"Redis 模块只支持 Redis 命令/键浏览，收到 %q", q.Language)
	}
	if strings.TrimSpace(q.Sort) != "" {
		return nil, domain.NewError(domain.CodeUnsupported, "redis.query",
			"Redis 数据浏览暂不支持 sort 条件，可用 --filter 作为键匹配模式")
	}
	client, err := e.session.clientFor(q.Database)
	if err != nil {
		return nil, err
	}
	if text := strings.TrimSpace(q.Text); text != "" {
		args, err := splitCommand(text)
		if err != nil {
			return nil, err
		}
		if q.ReadOnly && isWriteCommand(args) {
			return nil, domain.NewError(domain.CodeReadOnly, "redis.query",
				"只读连接禁止执行写命令 %q", strings.ToUpper(args[0].(string)))
		}
		return runCommand(ctx, client, args)
	}
	if q.Ref.Name != "" {
		return e.readKey(ctx, client, q.Ref.Name, q.MaxRows)
	}
	return e.browseKeys(ctx, client, q.Filter, q.MaxRows)
}

func runCommand(ctx context.Context, client *goredis.Client, args []any) (domain.ResultSet, error) {
	value, err := client.Do(ctx, args...).Result()
	if err != nil {
		return nil, mapQueryError(err)
	}
	cols := []domain.Column{{Name: "result", Type: "text"}}
	var rows []domain.Row
	switch list := value.(type) {
	case []any:
		cols = []domain.Column{{Name: "#", Type: "number"}, {Name: "value", Type: "text"}}
		rows = make([]domain.Row, 0, len(list))
		for i, item := range list {
			rows = append(rows, sqlkit.TextRow(strconv.Itoa(i+1), formatValue(item)))
		}
	default:
		rows = []domain.Row{sqlkit.TextRow(formatValue(value))}
	}
	return sqlkit.NewMemoryResultSet(cols, rows).WithAffected(1), nil
}

func (e *queryExecutor) Exec(ctx context.Context, statement, database string) (domain.ExecResult, error) {
	args, err := splitCommand(statement)
	if err != nil {
		return domain.ExecResult{}, err
	}
	client, err := e.session.clientFor(database)
	if err != nil {
		return domain.ExecResult{}, err
	}
	value, err := client.Do(ctx, args...).Result()
	if err != nil {
		return domain.ExecResult{}, mapQueryError(err)
	}
	return domain.ExecResult{
		Statement:    strings.TrimSpace(statement),
		RowsAffected: 1,
		Message:      "命令执行成功：" + formatValue(value),
	}, nil
}

func (e *queryExecutor) Explain(context.Context, domain.Query) (domain.ResultSet, error) {
	return nil, domain.NewError(domain.CodeUnsupported, "redis.explain",
		"Redis 不提供关系型执行计划；可用 MONITOR / SLOWLOG 命令分析")
}

func (e *queryExecutor) readKey(ctx context.Context, client *goredis.Client, key string, maxRows int) (domain.ResultSet, error) {
	limit := maxRows
	if limit <= 0 {
		limit = domain.DefaultMaxRows
	}
	typ, err := client.Type(ctx, key).Result()
	if err != nil {
		return nil, mapQueryError(err)
	}
	var (
		cols []domain.Column
		rows []domain.Row
	)
	switch strings.ToLower(typ) {
	case "none":
		cols = []domain.Column{{Name: "key"}, {Name: "type"}, {Name: "value"}}
		rows = []domain.Row{sqlkit.TextRow(key, "none", "(nil)")}
	case "string":
		value, getErr := client.Get(ctx, key).Result()
		if getErr != nil && getErr != goredis.Nil {
			return nil, mapQueryError(getErr)
		}
		cols = []domain.Column{{Name: "key"}, {Name: "value"}}
		rows = []domain.Row{sqlkit.TextRow(key, value)}
	case "hash":
		values, hashErr := client.HGetAll(ctx, key).Result()
		if hashErr != nil {
			return nil, mapQueryError(hashErr)
		}
		fields := make([]string, 0, len(values))
		for field := range values {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		cols = []domain.Column{{Name: "field"}, {Name: "value"}}
		for _, field := range fields {
			if len(rows) >= limit {
				break
			}
			rows = append(rows, sqlkit.TextRow(field, values[field]))
		}
	case "list":
		values, rangeErr := client.LRange(ctx, key, 0, int64(limit-1)).Result()
		if rangeErr != nil {
			return nil, mapQueryError(rangeErr)
		}
		cols = []domain.Column{{Name: "index"}, {Name: "value"}}
		for i, value := range values {
			rows = append(rows, sqlkit.TextRow(strconv.Itoa(i), value))
		}
	case "set":
		values, memberErr := client.SMembers(ctx, key).Result()
		if memberErr != nil {
			return nil, mapQueryError(memberErr)
		}
		sort.Strings(values)
		cols = []domain.Column{{Name: "member"}}
		for _, value := range values {
			if len(rows) >= limit {
				break
			}
			rows = append(rows, sqlkit.TextRow(value))
		}
	case "zset":
		values, rangeErr := client.ZRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
		if rangeErr != nil {
			return nil, mapQueryError(rangeErr)
		}
		cols = []domain.Column{{Name: "member"}, {Name: "score"}}
		for _, item := range values {
			rows = append(rows, sqlkit.TextRow(
				fmt.Sprint(item.Member), strconv.FormatFloat(item.Score, 'f', -1, 64)))
		}
	default:
		cols = []domain.Column{{Name: "key"}, {Name: "type"}, {Name: "value"}}
		rows = []domain.Row{sqlkit.TextRow(key, typ, "("+typ+" 类型请在查询页使用原生命令浏览)")}
	}
	return sqlkit.NewMemoryResultSet(cols, rows), nil
}

func (e *queryExecutor) browseKeys(ctx context.Context, client *goredis.Client, filter string, maxRows int) (domain.ResultSet, error) {
	limit := maxRows
	if limit <= 0 {
		limit = domain.DefaultMaxRows
	}
	keys, err := scanKeys(ctx, client, filter, limit)
	if err != nil {
		return nil, err
	}
	types := keyTypes(ctx, client, keys)
	pipe := client.Pipeline()
	ttls := make([]*goredis.DurationCmd, len(keys))
	values := make([]*goredis.StringCmd, len(keys))
	for i, key := range keys {
		ttls[i] = pipe.TTL(ctx, key)
		if types[i] == "string" {
			values[i] = pipe.Get(ctx, key)
		}
	}
	_, _ = pipe.Exec(ctx)
	cols := []domain.Column{
		{Name: "key", Type: "text"},
		{Name: "type", Type: "text"},
		{Name: "ttl", Type: "text"},
		{Name: "value", Type: "text"},
	}
	rows := make([]domain.Row, 0, len(keys))
	for i, key := range keys {
		ttl := ""
		if ttls[i] != nil && ttls[i].Err() == nil {
			ttl = ttls[i].Val().String()
		}
		value := "(" + types[i] + ")"
		if values[i] != nil && values[i].Err() == nil {
			value = values[i].Val()
		}
		rows = append(rows, sqlkit.TextRow(key, types[i], ttl, value))
	}
	return sqlkit.NewMemoryResultSet(cols, rows), nil
}
