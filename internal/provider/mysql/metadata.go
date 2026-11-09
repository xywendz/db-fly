package mysql

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

type metadataBrowser struct {
	db *sql.DB
}

var systemDatabases = map[string]bool{
	"information_schema": true,
	"mysql":              true,
	"performance_schema": true,
	"sys":                true,
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT SCHEMA_NAME,
       IFNULL(DEFAULT_CHARACTER_SET_NAME, ''),
       IFNULL(DEFAULT_COLLATION_NAME, '')
FROM information_schema.SCHEMATA
ORDER BY SCHEMA_NAME`)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.DatabaseInfo, 0, len(data))
	for _, r := range data {
		out = append(out, domain.DatabaseInfo{
			Name: r[0], Charset: r[1], Collation: r[2], System: systemDatabases[r[0]],
		})
	}
	return out, nil
}

// ListSchemas 在 MySQL 中返回空列表：database 与 schema 是同一层级，
// 能力声明 CapSchemas=false，表现层不应渲染额外节点。
func (b *metadataBrowser) ListSchemas(context.Context, string) ([]string, error) {
	return nil, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, _ string) ([]domain.ObjectInfo, error) {
	if database == "" {
		return nil, domain.NewError(domain.CodeValidation, "mysql.metadata", "未指定数据库")
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT TABLE_NAME, TABLE_TYPE, IFNULL(TABLE_COMMENT, ''), IFNULL(TABLE_ROWS, 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME`, database)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.ObjectInfo, 0, len(data))
	for _, r := range data {
		t := domain.ObjectTable
		if strings.Contains(strings.ToUpper(r[1]), "VIEW") {
			t = domain.ObjectView
		}
		n, _ := strconv.ParseInt(r[3], 10, 64)
		out = append(out, domain.ObjectInfo{
			Database: database, Name: r[0], Type: t, Comment: r[2], Rows: n,
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "mysql.metadata", "未指定表名")
	}
	schema := domain.TableSchema{Ref: ref}

	columns, err := b.columns(ctx, ref)
	if err != nil {
		return schema, err
	}
	schema.Columns = columns

	indexes, err := b.indexes(ctx, ref)
	if err != nil {
		return schema, err
	}
	schema.Indexes = indexes

	foreignKeys, err := b.foreignKeys(ctx, ref)
	if err != nil {
		return schema, err
	}
	schema.ForeignKeys = foreignKeys

	if err := b.tableOptions(ctx, ref, &schema); err != nil {
		return schema, err
	}
	if ddl, err := (&schemaEditor{db: b.db}).ShowCreate(ctx, ref); err == nil {
		schema.DDL = ddl
	}
	return schema, nil
}

func (b *metadataBrowser) columns(ctx context.Context, ref domain.ObjectRef) ([]domain.ColumnInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT ORDINAL_POSITION, COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE,
       IFNULL(COLUMN_DEFAULT, ''), IFNULL(EXTRA, ''), IFNULL(COLUMN_COMMENT, ''),
       IFNULL(CHARACTER_MAXIMUM_LENGTH, 0), IFNULL(COLUMN_KEY, ''),
       IF(COLUMN_DEFAULT IS NULL, 0, 1)
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`, ref.Database, ref.Name)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.ColumnInfo, 0, len(data))
	for _, r := range data {
		pos, _ := strconv.Atoi(r[0])
		length, _ := strconv.ParseInt(r[8], 10, 64)
		out = append(out, domain.ColumnInfo{
			Position:      pos,
			Name:          r[1],
			RawType:       r[2],
			DataType:      r[3],
			Nullable:      strings.EqualFold(r[4], "YES"),
			Default:       r[5],
			HasDefault:    r[10] == "1",
			AutoIncrement: strings.Contains(strings.ToLower(r[6]), "auto_increment"),
			Comment:       r[7],
			Length:        length,
			PrimaryKey:    r[9] == "PRI",
		})
	}
	return out, nil
}

func (b *metadataBrowser) indexes(ctx context.Context, ref domain.ObjectRef) ([]domain.IndexInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE, SEQ_IN_INDEX, IFNULL(INDEX_TYPE, '')
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY INDEX_NAME, SEQ_IN_INDEX`, ref.Database, ref.Name)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, mapQueryError(err)
	}
	var out []domain.IndexInfo
	position := make(map[string]int)
	for _, r := range data {
		name := r[0]
		idx, ok := position[name]
		if !ok {
			out = append(out, domain.IndexInfo{
				Name:      name,
				Columns:   []string{},
				Unique:    r[2] == "0",
				Primary:   name == "PRIMARY",
				IndexType: r[4],
			})
			idx = len(out) - 1
			position[name] = idx
		}
		out[idx].Columns = append(out[idx].Columns, r[1])
	}
	return out, nil
}

func (b *metadataBrowser) foreignKeys(ctx context.Context, ref domain.ObjectRef) ([]domain.ForeignKeyInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT CONSTRAINT_NAME, COLUMN_NAME,
       IFNULL(REFERENCED_TABLE_SCHEMA, ''), IFNULL(REFERENCED_TABLE_NAME, ''),
       IFNULL(REFERENCED_COLUMN_NAME, '')
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`, ref.Database, ref.Name)
	if err != nil {
		// 部分受管实例不开放该视图，降级为无外键信息。
		return nil, nil
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, nil
	}
	var out []domain.ForeignKeyInfo
	position := make(map[string]int)
	for _, r := range data {
		idx, ok := position[r[0]]
		if !ok {
			out = append(out, domain.ForeignKeyInfo{
				Name: r[0], RefDatabase: r[2], RefTable: r[3],
			})
			idx = len(out) - 1
			position[r[0]] = idx
		}
		out[idx].Columns = append(out[idx].Columns, r[1])
		out[idx].RefColumns = append(out[idx].RefColumns, r[4])
	}
	return out, nil
}

func (b *metadataBrowser) tableOptions(ctx context.Context, ref domain.ObjectRef, schema *domain.TableSchema) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT IFNULL(ENGINE, ''), IFNULL(TABLE_COLLATION, ''), IFNULL(TABLE_COMMENT, '')
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, ref.Database, ref.Name)
	if err != nil {
		return mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return mapQueryError(err)
	}
	if len(data) == 0 {
		return domain.NewError(domain.CodeNotFound, "mysql.metadata",
			"表 %s 不存在", ref.String())
	}
	schema.Engine = data[0][0]
	schema.Collation = data[0][1]
	schema.Comment = data[0][2]
	if i := strings.Index(schema.Collation, "_"); i > 0 {
		schema.Charset = schema.Collation[:i]
	}
	return nil
}
