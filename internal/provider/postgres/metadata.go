package postgres

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

// metadataBrowser 从 information_schema / pg_catalog 读取只读元数据。
type metadataBrowser struct {
	cfg   domain.ConnectionConfig
	pools *sqlkit.PoolSet
}

func (b *metadataBrowser) pool(ctx context.Context, database string) (*sql.DB, error) {
	return b.pools.DB(ctx, database)
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	db, err := b.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT datname, pg_encoding_to_char(encoding), COALESCE(datcollate, '')
FROM pg_database
WHERE NOT datistemplate
ORDER BY datname`)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.DatabaseInfo, 0, len(data))
	for _, r := range data {
		out = append(out, domain.DatabaseInfo{
			Name: r[0], Charset: r[1], Collation: r[2],
			System: r[0] == "postgres",
		})
	}
	return out, nil
}

func (b *metadataBrowser) ListSchemas(ctx context.Context, database string) ([]string, error) {
	db, err := b.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT schema_name FROM information_schema.schemata
WHERE schema_name NOT LIKE 'pg_%' AND schema_name <> 'information_schema'
ORDER BY schema_name`)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]string, 0, len(data))
	for _, r := range data {
		out = append(out, r[0])
	}
	return out, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, schema string) ([]domain.ObjectInfo, error) {
	db, err := b.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND (CAST($1 AS text) = '' OR table_schema = CAST($1 AS text))
ORDER BY table_schema, table_name`, schema)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.ObjectInfo, 0, len(data))
	for _, r := range data {
		typ := domain.ObjectTable
		if strings.EqualFold(r[2], "VIEW") {
			typ = domain.ObjectView
		}
		name := r[1]
		if r[0] != "public" {
			// 树形浏览器只传 database+name，非默认 schema 编码进 name，由方言层拆分引用。
			name = r[0] + "." + r[1]
		}
		out = append(out, domain.ObjectInfo{
			Database: database, Schema: r[0], Name: name, Type: typ,
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "postgresql.metadata", "未指定表名")
	}
	db, err := b.pool(ctx, ref.Database)
	if err != nil {
		return domain.TableSchema{}, err
	}
	owner, table := splitRef(ref)
	if owner == "" {
		owner = "public"
	}
	schema := domain.TableSchema{Ref: ref, Engine: "PostgreSQL"}

	columns, err := b.columns(ctx, db, owner, table)
	if err != nil {
		return schema, err
	}
	if len(columns) == 0 {
		return schema, domain.NewError(domain.CodeNotFound, "postgresql.metadata",
			"对象 %s 不存在", ref.String())
	}
	schema.Columns = columns
	schema.Indexes = b.indexes(ctx, db, owner, table)
	schema.ForeignKeys = b.foreignKeys(ctx, db, owner, table)
	qualifiedName, err := ansi.Qualified(owner, table)
	if err == nil {
		schema.DDL = sqlkit.RenderCreateTable(ansi, schema, qualifiedName,
			"由 information_schema 元数据反向生成，可能与服务端原始 DDL 不完全一致")
	}
	return schema, nil
}

func (b *metadataBrowser) columns(ctx context.Context, db *sql.DB, owner, table string) ([]domain.ColumnInfo, error) {
	rows, err := db.QueryContext(ctx, `
SELECT ordinal_position, column_name, data_type, udt_name, is_nullable,
       COALESCE(column_default, ''), COALESCE(character_maximum_length, 0),
       (column_default IS NOT NULL)::text,
       (column_default LIKE 'nextval%')::text
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`, owner, table)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	pk, err := b.primaryKeys(ctx, db, owner, table)
	if err != nil {
		pk = nil
	}
	out := make([]domain.ColumnInfo, 0, len(data))
	for _, r := range data {
		pos, _ := strconv.Atoi(r[0])
		length, _ := strconv.ParseInt(r[6], 10, 64)
		out = append(out, domain.ColumnInfo{
			Position:      pos,
			Name:          r[1],
			DataType:      r[2],
			RawType:       r[3],
			Nullable:      strings.EqualFold(r[4], "YES"),
			Default:       r[5],
			HasDefault:    r[7] == "true",
			Length:        length,
			AutoIncrement: r[8] == "true",
			PrimaryKey:    pk[r[1]],
		})
	}
	return out, nil
}

func (b *metadataBrowser) primaryKeys(ctx context.Context, db *sql.DB, owner, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
WHERE tc.constraint_type = 'PRIMARY KEY' AND tc.table_schema = $1 AND tc.table_name = $2`, owner, table)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make(map[string]bool, len(data))
	for _, r := range data {
		out[r[0]] = true
	}
	return out, nil
}

func (b *metadataBrowser) indexes(ctx context.Context, db *sql.DB, owner, table string) []domain.IndexInfo {
	rows, err := db.QueryContext(ctx, `
SELECT indexname, indexdef FROM pg_indexes
WHERE schemaname = $1 AND tablename = $2
ORDER BY indexname`, owner, table)
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	out := make([]domain.IndexInfo, 0, len(data))
	for _, r := range data {
		def := strings.ToUpper(r[1])
		out = append(out, domain.IndexInfo{
			Name:      r[0],
			Columns:   indexColumns(r[1]),
			Unique:    strings.Contains(def, "CREATE UNIQUE INDEX"),
			Primary:   strings.HasSuffix(r[0], "_pkey"),
			IndexType: "index",
		})
	}
	return out
}

// indexColumns 从 CREATE INDEX ... USING btree (a, b) 中粗提取列名。
func indexColumns(def string) []string {
	start := strings.Index(def, "(")
	end := strings.LastIndex(def, ")")
	if start < 0 || end <= start {
		return nil
	}
	parts := strings.Split(def[start+1:end], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.Fields(strings.TrimSpace(part))
		if len(field) == 0 {
			continue
		}
		out = append(out, strings.Trim(field[0], `"`))
	}
	return out
}

func (b *metadataBrowser) foreignKeys(ctx context.Context, db *sql.DB, owner, table string) []domain.ForeignKeyInfo {
	rows, err := db.QueryContext(ctx, `
SELECT tc.constraint_name, kcu.column_name,
       COALESCE(ccu.table_schema, ''), COALESCE(ccu.table_name, ''), COALESCE(ccu.column_name, '')
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
LEFT JOIN information_schema.constraint_column_usage ccu
  ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = $1 AND tc.table_name = $2
ORDER BY tc.constraint_name, kcu.ordinal_position`, owner, table)
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
		i, ok := position[r[0]]
		if !ok {
			out = append(out, domain.ForeignKeyInfo{
				Name: r[0], RefDatabase: r[2], RefTable: r[3],
			})
			i = len(out) - 1
			position[r[0]] = i
		}
		out[i].Columns = append(out[i].Columns, r[1])
		out[i].RefColumns = append(out[i].RefColumns, r[4])
	}
	return out
}
