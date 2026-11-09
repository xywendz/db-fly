package oracle

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/provider/sqlkit"
)

var oracleSystemSchemas = map[string]bool{
	"SYS": true, "SYSTEM": true, "OUTLN": true, "DBSNMP": true,
	"APPQOSSYS": true, "CTXSYS": true, "XDB": true, "MDSYS": true,
	"ORDSYS": true, "WMSYS": true, "LBACSYS": true, "DVSYS": true,
}

type metadataBrowser struct {
	db *sql.DB
}

func namedOwner(owner string) any { return sql.Named("owner", strings.ToUpper(owner)) }

func namedTable(table string) any { return sql.Named("table", table) }

func (b *metadataBrowser) ListDatabases(context.Context) ([]domain.DatabaseInfo, error) {
	rows, err := b.db.Query(`SELECT username FROM all_users ORDER BY username`)
	if err != nil {
		return nil, sqlkit.MapError("oracle.metadata", err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, sqlkit.MapError("oracle.metadata", err)
	}
	out := make([]domain.DatabaseInfo, 0, len(data))
	for _, r := range data {
		out = append(out, domain.DatabaseInfo{
			Name: r[0], System: oracleSystemSchemas[strings.ToUpper(r[0])],
		})
	}
	return out, nil
}

func (b *metadataBrowser) ListSchemas(ctx context.Context, _ string) ([]string, error) {
	list, err := b.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.Name)
	}
	return out, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, schema string) ([]domain.ObjectInfo, error) {
	owner := schema
	if owner == "" {
		owner = database
	}
	if owner == "" {
		return nil, domain.NewError(domain.CodeValidation, "oracle.metadata", "未指定 schema")
	}
	rows, err := b.db.QueryContext(ctx, `
SELECT object_name, object_type FROM (
    SELECT table_name AS object_name, 'TABLE' AS object_type, owner FROM all_tables
    UNION ALL
    SELECT view_name, 'VIEW', owner FROM all_views
) WHERE owner = :owner
ORDER BY object_name`, namedOwner(owner))
	if err != nil {
		return nil, sqlkit.MapError("oracle.metadata", err)
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, sqlkit.MapError("oracle.metadata", err)
	}
	out := make([]domain.ObjectInfo, 0, len(data))
	for _, r := range data {
		typ := domain.ObjectTable
		if r[1] == "VIEW" {
			typ = domain.ObjectView
		}
		out = append(out, domain.ObjectInfo{
			Database: database, Schema: owner, Name: r[0], Type: typ,
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "oracle.metadata", "未指定表名")
	}
	schema := domain.TableSchema{Ref: ref, Engine: "Oracle"}
	owner := ownerOf(ref)
	if owner == "" {
		owner = b.currentUser(ctx)
	}
	pk := b.primaryKeys(ctx, owner, ref.Name)
	columns, err := b.columns(ctx, owner, ref.Name, pk)
	if err != nil {
		return schema, err
	}
	if len(columns) == 0 {
		return schema, domain.NewError(domain.CodeNotFound, "oracle.metadata",
			"对象 %s 不存在", ref.String())
	}
	schema.Columns = columns
	schema.Indexes = b.indexes(ctx, owner, ref.Name)
	qualifiedName, qerr := ansi.Qualified(owner, ref.Name)
	if qerr == nil {
		schema.DDL = sqlkit.RenderCreateTable(ansi, schema, qualifiedName,
			"由 all_tab_columns 元数据反向生成")
	}
	return schema, nil
}

func (b *metadataBrowser) currentUser(ctx context.Context) string {
	var user string
	if err := b.db.QueryRowContext(ctx, "SELECT USER FROM DUAL").Scan(&user); err != nil {
		return ""
	}
	return user
}

func (b *metadataBrowser) columns(ctx context.Context, owner, table string, pk map[string]bool) ([]domain.ColumnInfo, error) {
	// data_default 是 LONG 类型，无法参与函数运算，因此不在元数据查询中读取默认值。
	query := `
SELECT column_id, column_name, data_type, data_type, nullable,
       '', COALESCE(data_length, 0), 0,
       COALESCE(identity_column, 'NO')
FROM all_tab_columns
WHERE owner = :owner AND table_name = :table
ORDER BY column_id`
	rows, err := b.db.QueryContext(ctx, query, namedOwner(owner), namedTable(table))
	if err != nil {
		// Oracle 11g 没有 identity_column 列，降级查询。
		query = strings.Replace(query, ",\n       COALESCE(identity_column, 'NO')", "", 1)
		rows, err = b.db.QueryContext(ctx, query, namedOwner(owner), namedTable(table))
		if err != nil {
			return nil, sqlkit.MapError("oracle.metadata", err)
		}
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil, sqlkit.MapError("oracle.metadata", err)
	}
	out := make([]domain.ColumnInfo, 0, len(data))
	for _, r := range data {
		pos, _ := strconv.Atoi(r[0])
		length, _ := strconv.ParseInt(r[6], 10, 64)
		auto := len(r) > 8 && strings.EqualFold(r[8], "YES")
		typ := r[2]
		if length > 0 && (strings.Contains(strings.ToUpper(typ), "CHAR") ||
			strings.Contains(strings.ToUpper(typ), "NCHAR")) {
			typ += "(" + strconv.FormatInt(length, 10) + ")"
		}
		out = append(out, domain.ColumnInfo{
			Position: pos, Name: r[1], DataType: r[2], RawType: typ,
			Nullable: strings.EqualFold(r[4], "Y"), Default: r[5],
			HasDefault: r[7] == "1", Length: length,
			AutoIncrement: auto, PrimaryKey: pk[r[1]],
		})
	}
	return out, nil
}

func (b *metadataBrowser) primaryKeys(ctx context.Context, owner, table string) map[string]bool {
	rows, err := b.db.QueryContext(ctx, `
SELECT acc.column_name
FROM all_constraints ac
JOIN all_cons_columns acc
  ON ac.constraint_name = acc.constraint_name AND ac.owner = acc.owner
WHERE ac.constraint_type = 'P' AND ac.owner = :owner AND ac.table_name = :table`,
		namedOwner(owner), namedTable(table))
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(data))
	for _, r := range data {
		out[r[0]] = true
	}
	return out
}

func (b *metadataBrowser) indexes(ctx context.Context, owner, table string) []domain.IndexInfo {
	rows, err := b.db.QueryContext(ctx, `
SELECT i.index_name, c.column_name, i.uniqueness, i.index_type, c.column_position
FROM all_indexes i
JOIN all_ind_columns c ON i.index_name = c.index_name AND i.owner = c.index_owner
WHERE i.owner = :owner AND i.table_name = :table
ORDER BY i.index_name, c.column_position`, namedOwner(owner), namedTable(table))
	if err != nil {
		return nil
	}
	data, err := sqlkit.ScanAllStrings(rows)
	if err != nil {
		return nil
	}
	var out []domain.IndexInfo
	position := make(map[string]int)
	for _, r := range data {
		i, ok := position[r[0]]
		if !ok {
			out = append(out, domain.IndexInfo{
				Name: r[0], Unique: r[2] == "UNIQUE", IndexType: r[3],
			})
			i = len(out) - 1
			position[r[0]] = i
		}
		out[i].Columns = append(out[i].Columns, r[1])
	}
	return out
}
