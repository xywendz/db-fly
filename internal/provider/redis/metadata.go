package redis

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/db-fly/db-fly/internal/domain"
)

const (
	maxTreeKeys  = 2000
	maxScanCount = 500
)

// scanKeys 使用 SCAN 游标分批读取键，避免 KEYS * 阻塞服务端。
func scanKeys(ctx context.Context, client *goredis.Client, pattern string, limit int) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "*"
	}
	if limit <= 0 {
		limit = maxTreeKeys
	}
	var (
		keys   []string
		cursor uint64
	)
	for {
		batch, next, err := client.Scan(ctx, cursor, pattern, maxScanCount).Result()
		if err != nil {
			return nil, mapQueryError(err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 || len(keys) >= limit {
			break
		}
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

// keyTypes 通过一次 pipeline 批量读取键类型，避免逐键往返。
func keyTypes(ctx context.Context, client *goredis.Client, keys []string) []string {
	types := make([]string, len(keys))
	pipe := client.Pipeline()
	cmds := make([]*goredis.StatusCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Type(ctx, key)
	}
	_, _ = pipe.Exec(ctx)
	for i, cmd := range cmds {
		types[i] = cmd.Val()
		if types[i] == "" {
			types[i] = "none"
		}
	}
	return types
}

type metadataBrowser struct {
	session *session
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	count := 16
	if config, err := b.session.base.ConfigGet(ctx, "databases").Result(); err == nil {
		if raw, ok := config["databases"]; ok {
			if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
				count = parsed
			}
		}
	}
	if info, err := b.session.base.Info(ctx, "keyspace").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "db") || !strings.Contains(line, ":") {
				continue
			}
			indexText := strings.SplitN(line, ":", 2)[0]
			index, convErr := strconv.Atoi(strings.TrimPrefix(indexText, "db"))
			if convErr == nil && index+1 > count {
				count = index + 1
			}
		}
	}
	if count > 256 {
		count = 256
	}
	out := make([]domain.DatabaseInfo, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, domain.DatabaseInfo{
			Name: "db" + strconv.Itoa(i), Charset: "", System: false,
		})
	}
	return out, nil
}

func (b *metadataBrowser) ListSchemas(context.Context, string) ([]string, error) {
	return nil, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, _ string) ([]domain.ObjectInfo, error) {
	client, err := b.session.clientFor(database)
	if err != nil {
		return nil, err
	}
	keys, err := scanKeys(ctx, client, "*", maxTreeKeys)
	if err != nil {
		return nil, err
	}
	types := keyTypes(ctx, client, keys)
	out := make([]domain.ObjectInfo, 0, len(keys))
	for i, key := range keys {
		out = append(out, domain.ObjectInfo{
			Database: database, Name: key, Type: domain.ObjectKey, Comment: types[i],
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "redis.metadata", "未指定键名")
	}
	client, err := b.session.clientFor(ref.Database)
	if err != nil {
		return domain.TableSchema{}, err
	}
	typ, err := client.Type(ctx, ref.Name).Result()
	if err != nil {
		return domain.TableSchema{}, mapQueryError(err)
	}
	ttl, _ := client.TTL(ctx, ref.Name).Result()
	value := readKeyPreview(ctx, client, typ, ref.Name, 20)
	schema := domain.TableSchema{
		Ref: ref, Engine: "Redis", Charset: ref.Database,
		Columns: []domain.ColumnInfo{
			{Position: 1, Name: "key", DataType: "string", RawType: "string", PrimaryKey: true},
			{Position: 2, Name: "type", DataType: "string", RawType: "string"},
			{Position: 3, Name: "ttl", DataType: "integer", RawType: "integer"},
			{Position: 4, Name: "value", DataType: "string", RawType: "string"},
		},
		Comment: fmt.Sprintf("%s · TTL %s", typ, ttl.String()),
	}
	schema.DDL = "# Redis 键没有建表语句，以下为等价创建命令\n" +
		"SET " + strconv.Quote(ref.Name) + " " + strconv.Quote(value)
	return schema, nil
}

func readKeyPreview(ctx context.Context, client *goredis.Client, typ, key string, limit int) string {
	switch strings.ToLower(typ) {
	case "string":
		value, err := client.Get(ctx, key).Result()
		if err != nil {
			return ""
		}
		return value
	case "hash":
		values, err := client.HGetAll(ctx, key).Result()
		if err != nil {
			return ""
		}
		parts := make([]string, 0, len(values))
		for field, value := range values {
			parts = append(parts, field+"="+value)
		}
		return strings.Join(parts, " ")
	case "list":
		values, _ := client.LRange(ctx, key, 0, int64(limit-1)).Result()
		return strings.Join(values, " ")
	case "set":
		values, _ := client.SMembers(ctx, key).Result()
		return strings.Join(values, " ")
	case "zset":
		values, _ := client.ZRange(ctx, key, 0, int64(limit-1)).Result()
		return strings.Join(values, " ")
	default:
		return ""
	}
}
