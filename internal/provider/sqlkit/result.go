package sqlkit

import (
	"database/sql"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// RowsResultSet 是基于 *sql.Rows 的流式结果集，兼容任意 database/sql 驱动。
// 若传入 conn（为切换库而独占的连接），Close 时一并归还。
type RowsResultSet struct {
	rows     *sql.Rows
	conn     *sql.Conn
	op       string
	cols     []domain.Column
	binary   []bool
	current  domain.Row
	err      error
	affected int64
	messages []domain.ServerMessage
}

// NewRowsResultSet 创建流式结果集；op 用于错误信息前缀。
func NewRowsResultSet(rows *sql.Rows, conn *sql.Conn, op string) (*RowsResultSet, error) {
	rs := &RowsResultSet{rows: rows, conn: conn, op: op}
	if err := rs.refreshColumns(); err != nil {
		return nil, err
	}
	return rs, nil
}

func (rs *RowsResultSet) refreshColumns() error {
	names, err := rs.rows.Columns()
	if err != nil {
		return MapError(rs.op, err)
	}
	types, typeErr := rs.rows.ColumnTypes()
	cols := make([]domain.Column, len(names))
	binary := make([]bool, len(names))
	for i, name := range names {
		typeName := ""
		if typeErr == nil && i < len(types) {
			typeName = types[i].DatabaseTypeName()
		}
		cols[i] = domain.Column{Name: name, Type: typeName}
		binary[i] = isBinaryType(typeName)
	}
	rs.cols, rs.binary = cols, binary
	return nil
}

func (rs *RowsResultSet) Columns() []domain.Column {
	out := make([]domain.Column, len(rs.cols))
	copy(out, rs.cols)
	return out
}

func (rs *RowsResultSet) Next() bool {
	if !rs.rows.Next() {
		if err := rs.rows.Err(); err != nil {
			rs.err = MapError(rs.op, err)
		}
		return false
	}
	values := make([]any, len(rs.cols))
	ptrs := make([]any, len(rs.cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rs.rows.Scan(ptrs...); err != nil {
		rs.err = MapError(rs.op, err)
		return false
	}
	row := make(domain.Row, len(rs.cols))
	for i, v := range values {
		if v == nil {
			row[i] = domain.NewNullCell()
			continue
		}
		if raw, ok := v.([]byte); ok && raw != nil && rs.binary[i] {
			row[i] = domain.NewBinaryCell(append([]byte(nil), raw...))
			continue
		}
		row[i] = domain.NewTextCell(Stringify(v))
	}
	rs.current = row
	return true
}

func (rs *RowsResultSet) Row() domain.Row                  { return rs.current }
func (rs *RowsResultSet) Document() string                 { return "" }
func (rs *RowsResultSet) Err() error                       { return rs.err }
func (rs *RowsResultSet) Affected() int64                  { return rs.affected }
func (rs *RowsResultSet) Messages() []domain.ServerMessage { return rs.messages }

func (rs *RowsResultSet) NextResultSet() bool {
	if !rs.rows.NextResultSet() {
		return false
	}
	if err := rs.refreshColumns(); err != nil {
		rs.err = err
		return false
	}
	rs.current = nil
	return true
}

func (rs *RowsResultSet) Close() error {
	err := rs.rows.Close()
	if rs.conn != nil {
		if closeErr := rs.conn.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func isBinaryType(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{
		"BLOB", "BINARY", "VARBINARY", "BYTEA", "BIT", "RAW", "GEOMETRY",
		"POINT", "LINESTRING", "POLYGON",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// MemoryResultSet 用于命令式结果（例如 Redis），把已构造好的行按流式接口暴露。
type MemoryResultSet struct {
	cols       []domain.Column
	rows       []domain.Row
	docs       []string
	affected   int64
	messages   []domain.ServerMessage
	index      int
	current    domain.Row
	currentDoc string
	err        error
}

// NewMemoryResultSet 创建内存结果集。
func NewMemoryResultSet(cols []domain.Column, rows []domain.Row) *MemoryResultSet {
	return &MemoryResultSet{cols: cols, rows: rows}
}

// WithDocuments 附加原始 JSON 文档（供 JSON 树视图使用）。
func (rs *MemoryResultSet) WithDocuments(docs []string) *MemoryResultSet {
	rs.docs = docs
	return rs
}

// WithAffected 设置影响行数与服务端消息。
func (rs *MemoryResultSet) WithAffected(n int64, messages ...domain.ServerMessage) *MemoryResultSet {
	rs.affected = n
	rs.messages = messages
	return rs
}

func (rs *MemoryResultSet) Columns() []domain.Column {
	out := make([]domain.Column, len(rs.cols))
	copy(out, rs.cols)
	return out
}

func (rs *MemoryResultSet) Next() bool {
	if rs.index >= len(rs.rows) {
		return false
	}
	rs.current = rs.rsRow(rs.index)
	if rs.index < len(rs.docs) {
		rs.currentDoc = rs.docs[rs.index]
	} else {
		rs.currentDoc = ""
	}
	rs.index++
	return true
}

func (rs *MemoryResultSet) rsRow(i int) domain.Row {
	row := make(domain.Row, len(rs.rows[i]))
	copy(row, rs.rows[i])
	return row
}

func (rs *MemoryResultSet) Row() domain.Row                  { return rs.current }
func (rs *MemoryResultSet) Document() string                 { return rs.currentDoc }
func (rs *MemoryResultSet) Err() error                       { return rs.err }
func (rs *MemoryResultSet) NextResultSet() bool              { return false }
func (rs *MemoryResultSet) Affected() int64                  { return rs.affected }
func (rs *MemoryResultSet) Messages() []domain.ServerMessage { return rs.messages }
func (rs *MemoryResultSet) Close() error                     { return nil }

// TextRow 用字符串快速构造一行结果。
func TextRow(values ...string) domain.Row {
	row := make(domain.Row, len(values))
	for i, v := range values {
		row[i] = domain.NewTextCell(v)
	}
	return row
}
