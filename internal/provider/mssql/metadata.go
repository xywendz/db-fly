package mssql

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/db-fly/db-fly/internal/domain"
)

type metadataBrowser struct {
	db *sql.DB
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT name, ISNULL(collation_name, ''),
       CASE WHEN name IN ('master', 'model', 'msdb', 'tempdb') THEN 1 ELSE 0 END
FROM sys.databases ORDER BY name`)
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
			Name: r[0], Collation: r[1], System: r[2] == "1",
		})
	}
	return out, nil
}

func (b *metadataBrowser) ListSchemas(ctx context.Context, database string) ([]string, error) {
	db, err := quoteIdent(database)
	if err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT name FROM `+db+`.sys.schemas
WHERE name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest')
  AND name NOT LIKE 'db[_]%'
ORDER BY name`)
	if err != nil {
		return nil, mapQueryError(err)
	}
	data, err := scanAllStrings(rows)
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
	db, err := quoteIdent(database)
	if err != nil {
		return nil, err
	}
	filter := ""
	if schema != "" {
		if _, err := quoteIdent(schema); err != nil {
			return nil, err
		}
		filter = " AND s.name = N'" + escapeLiteral(schema) + "'"
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT s.name, o.name, o.type
FROM `+db+`.sys.objects o
JOIN `+db+`.sys.schemas s ON o.schema_id = s.schema_id
WHERE o.type IN ('U', 'V')`+filter+`
ORDER BY s.name, o.name`)
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
		if r[2] == "V" {
			t = domain.ObjectView
		}
		out = append(out, domain.ObjectInfo{
			Database: database, Schema: r[0], Name: r[1], Type: t,
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	schemaName := defaultSchema(ref.Schema)
	db, err := quoteIdent(ref.Database)
	if err != nil {
		return domain.TableSchema{}, err
	}
	qualified, err := quoteQualified(ref.Database, schemaName, ref.Name)
	if err != nil {
		return domain.TableSchema{}, err
	}
	objectID := "OBJECT_ID(N'" + escapeLiteral(qualified) + "')"
	result := domain.TableSchema{
		Ref: domain.ObjectRef{Database: ref.Database, Schema: schemaName, Name: ref.Name},
	}

	columns, err := b.columns(ctx, db, objectID)
	if err != nil {
		return result, err
	}
	result.Columns = columns

	indexes, err := b.indexes(ctx, db, objectID)
	if err != nil {
		return result, err
	}
	result.Indexes = indexes

	foreignKeys, err := b.foreignKeys(ctx, db, objectID)
	if err != nil {
		return result, err
	}
	result.ForeignKeys = foreignKeys

	if err := b.db.QueryRowContext(ctx,
		"SELECT ISNULL(collation_name, '') FROM sys.databases WHERE name = N'"+escapeLiteral(ref.Database)+"'").
		Scan(&result.Collation); err != nil {
		return result, mapQueryError(err)
	}
	result.DDL = renderCreateTable(result)
	return result, nil
}

func (b *metadataBrowser) columns(ctx context.Context, db, objectID string) ([]domain.ColumnInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT c.column_id, c.name, t.name, ISNULL(c.max_length, 0), c.is_nullable,
       ISNULL(dc.definition, ''), c.is_identity,
       ISNULL(CAST(ep.value AS nvarchar(4000)), ''),
       CASE WHEN pk.column_id IS NULL THEN 0 ELSE 1 END,
       ISNULL(c.collation_name, '')
FROM `+db+`.sys.columns c
JOIN `+db+`.sys.types t ON c.user_type_id = t.user_type_id
LEFT JOIN `+db+`.sys.default_constraints dc ON c.default_object_id = dc.object_id
LEFT JOIN `+db+`.sys.extended_properties ep
       ON ep.major_id = c.object_id AND ep.minor_id = c.column_id AND ep.name = 'MS_Description'
LEFT JOIN (
    SELECT ic.object_id, ic.column_id
    FROM `+db+`.sys.index_columns ic
    JOIN `+db+`.sys.indexes i ON i.object_id = ic.object_id AND i.index_id = ic.index_id
    WHERE i.is_primary_key = 1
) pk ON pk.object_id = c.object_id AND pk.column_id = c.column_id
WHERE c.object_id = `+objectID+`
ORDER BY c.column_id`)
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
		length, _ := strconv.ParseInt(r[3], 10, 64)
		out = append(out, domain.ColumnInfo{
			Position:      pos,
			Name:          r[1],
			DataType:      r[2],
			RawType:       r[2],
			Length:        length,
			Nullable:      r[4] == "1",
			Default:       r[5],
			HasDefault:    r[5] != "",
			AutoIncrement: r[6] == "1",
			Comment:       r[7],
			PrimaryKey:    r[8] == "1",
		})
	}
	return out, nil
}

func (b *metadataBrowser) indexes(ctx context.Context, db, objectID string) ([]domain.IndexInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT i.name, i.is_unique, i.is_primary_key, ISNULL(i.type_desc, ''), c.name
FROM `+db+`.sys.indexes i
JOIN `+db+`.sys.index_columns ic ON i.object_id = ic.object_id AND i.index_id = ic.index_id
JOIN `+db+`.sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.object_id = `+objectID+` AND i.name IS NOT NULL
ORDER BY i.name, ic.key_ordinal`)
	if err != nil {
		return nil, nil // 部分版本/权限下降级为无索引信息
	}
	data, err := scanAllStrings(rows)
	if err != nil {
		return nil, nil
	}
	var out []domain.IndexInfo
	position := make(map[string]int)
	for _, r := range data {
		idx, ok := position[r[0]]
		if !ok {
			out = append(out, domain.IndexInfo{
				Name: r[0], Unique: r[1] == "1", Primary: r[2] == "1", IndexType: r[3],
			})
			idx = len(out) - 1
			position[r[0]] = idx
		}
		out[idx].Columns = append(out[idx].Columns, r[4])
	}
	return out, nil
}

func (b *metadataBrowser) foreignKeys(ctx context.Context, db, objectID string) ([]domain.ForeignKeyInfo, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT fk.name, pc.name, rt.name, rc.name
FROM `+db+`.sys.foreign_keys fk
JOIN `+db+`.sys.foreign_key_columns fkc ON fk.object_id = fkc.constraint_object_id
JOIN `+db+`.sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN `+db+`.sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
JOIN `+db+`.sys.tables rt ON rt.object_id = fkc.referenced_object_id
WHERE fk.parent_object_id = `+objectID+`
ORDER BY fk.name, fkc.constraint_column_id`)
	if err != nil {
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
			out = append(out, domain.ForeignKeyInfo{Name: r[0], RefTable: r[2]})
			idx = len(out) - 1
			position[r[0]] = idx
		}
		out[idx].Columns = append(out[idx].Columns, r[1])
		out[idx].RefColumns = append(out[idx].RefColumns, r[3])
	}
	return out, nil
}
