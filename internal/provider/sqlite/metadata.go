package sqlite

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

type metadataBrowser struct {
	db *sql.DB
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	rows, err := b.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, sqlkit.MapError("sqlite.metadata", err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, sqlkit.MapError("sqlite.metadata", err)
	}
	out := make([]domain.DatabaseInfo, 0, len(data))
	for _, r := range data {
		if len(r) < 2 {
			continue
		}
		out = append(out, domain.DatabaseInfo{
			Name: r[1], System: r[1] == "temp",
			Charset: "UTF-8",
		})
	}
	return out, nil
}

func (b *metadataBrowser) ListSchemas(context.Context, string) ([]string, error) {
	return nil, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, _ string) ([]domain.ObjectInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT name, type FROM sqlite_master
WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	if err != nil {
		return nil, sqlkit.MapError("sqlite.metadata", err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, sqlkit.MapError("sqlite.metadata", err)
	}
	out := make([]domain.ObjectInfo, 0, len(data))
	for _, r := range data {
		typ := domain.ObjectTable
		if r[1] == "view" {
			typ = domain.ObjectView
		}
		out = append(out, domain.ObjectInfo{Database: database, Name: r[0], Type: typ})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "sqlite.metadata", "未指定表名")
	}
	schema := domain.TableSchema{Ref: ref, Engine: "SQLite"}
	quoted, err := ansi.Ident(ref.Name)
	if err != nil {
		return schema, err
	}
	rows, err := b.db.QueryContext(ctx, "PRAGMA table_info("+quoted+")")
	if err != nil {
		return schema, sqlkit.MapError("sqlite.metadata", err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return schema, sqlkit.MapError("sqlite.metadata", err)
	}
	if len(data) == 0 {
		return schema, domain.NewError(domain.CodeNotFound, "sqlite.metadata",
			"对象 %s 不存在", ref.String())
	}
	ddl := b.createSQL(ctx, ref.Name)
	for _, r := range data {
		pos, _ := strconv.Atoi(r[0])
		pkOrder, _ := strconv.Atoi(r[5])
		nullable := r[3] != "1" && pkOrder == 0
		schema.Columns = append(schema.Columns, domain.ColumnInfo{
			Position:      pos + 1,
			Name:          r[1],
			DataType:      r[2],
			RawType:       r[2],
			Nullable:      nullable,
			Default:       r[4],
			HasDefault:    r[4] != "",
			PrimaryKey:    pkOrder > 0,
			AutoIncrement: pkOrder > 0 && strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT"),
		})
	}
	schema.Indexes = b.indexes(ctx, ref.Name)
	schema.ForeignKeys = b.foreignKeys(ctx, ref.Name)
	schema.DDL = ddl
	return schema, nil
}

func (b *metadataBrowser) createSQL(ctx context.Context, name string) string {
	var raw any
	if err := b.db.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE name = ? AND type IN ('table','view')", name).Scan(&raw); err != nil {
		return ""
	}
	return trimSemicolon(sqlkit.Stringify(raw))
}

func (b *metadataBrowser) indexes(ctx context.Context, table string) []domain.IndexInfo {
	rows, err := b.db.QueryContext(ctx, "PRAGMA index_list("+pragmaArg(table)+")")
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	var out []domain.IndexInfo
	for _, r := range data {
		if len(r) < 4 {
			continue
		}
		info := domain.IndexInfo{
			Name: r[1], Unique: r[2] == "1", IndexType: r[3],
		}
		info.Columns = b.indexColumns(ctx, r[1])
		out = append(out, info)
	}
	return out
}

func (b *metadataBrowser) indexColumns(ctx context.Context, index string) []string {
	rows, err := b.db.QueryContext(ctx,
		"SELECT name FROM pragma_index_info("+pragmaArg(index)+") ORDER BY seqno")
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(data))
	for _, r := range data {
		out = append(out, r[0])
	}
	return out
}

// PRAGMA 不接受绑定参数，只能拼入经过转义的标识符字面量。
func pragmaArg(name string) string {
	return "'" + sqlkit.EscapeLiteral(name) + "'"
}

func (b *metadataBrowser) foreignKeys(ctx context.Context, table string) []domain.ForeignKeyInfo {
	rows, err := b.db.QueryContext(ctx, "PRAGMA foreign_key_list("+pragmaArg(table)+")")
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	var out []domain.ForeignKeyInfo
	position := make(map[string]int)
	for _, r := range data {
		if len(r) < 5 {
			continue
		}
		key := r[0]
		i, ok := position[key]
		if !ok {
			out = append(out, domain.ForeignKeyInfo{Name: "fk_" + key, RefTable: r[2]})
			i = len(out) - 1
			position[key] = i
		}
		out[i].Columns = append(out[i].Columns, r[3])
		out[i].RefColumns = append(out[i].RefColumns, r[4])
	}
	return out
}
