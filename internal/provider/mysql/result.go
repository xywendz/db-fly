package mysql

import (
	"database/sql"
	"strings"

	"github.com/dbclient/dbclient/internal/domain"
)

// resultSet 是流式结果集，每次 Next 都分配新的 Row，避免表现层持有失效缓冲区。
type resultSet struct {
	rows     *sql.Rows
	conn     *sql.Conn
	cols     []domain.Column
	binary   []bool
	current  domain.Row
	err      error
	messages []domain.ServerMessage
}

func newResultSet(rows *sql.Rows, conn *sql.Conn) (*resultSet, error) {
	rs := &resultSet{rows: rows, conn: conn}
	if err := rs.refreshColumns(); err != nil {
		return nil, err
	}
	return rs, nil
}

func (rs *resultSet) refreshColumns() error {
	types, err := rs.rows.ColumnTypes()
	if err != nil {
		return mapQueryError(err)
	}
	cols := make([]domain.Column, len(types))
	binary := make([]bool, len(types))
	for i, t := range types {
		name := t.DatabaseTypeName()
		cols[i] = domain.Column{Name: t.Name(), Type: name}
		binary[i] = isBinaryType(name)
	}
	rs.cols, rs.binary = cols, binary
	return nil
}

func (rs *resultSet) Columns() []domain.Column {
	out := make([]domain.Column, len(rs.cols))
	copy(out, rs.cols)
	return out
}

func (rs *resultSet) Next() bool {
	if !rs.rows.Next() {
		if err := rs.rows.Err(); err != nil {
			rs.err = mapQueryError(err)
		}
		return false
	}
	values := make([]any, len(rs.cols))
	raws := make([]*sql.RawBytes, len(rs.cols))
	for i := range raws {
		raws[i] = new(sql.RawBytes)
		values[i] = raws[i]
	}
	if err := rs.rows.Scan(values...); err != nil {
		rs.err = mapQueryError(err)
		return false
	}
	row := make(domain.Row, len(rs.cols))
	for i, p := range raws {
		if *p == nil {
			row[i] = domain.NewNullCell()
			continue
		}
		if rs.binary[i] {
			row[i] = domain.NewBinaryCell(append([]byte(nil), (*p)...))
		} else {
			row[i] = domain.NewTextCell(string(*p))
		}
	}
	rs.current = row
	return true
}

func (rs *resultSet) Row() domain.Row { return rs.current }

// Document 关系型结果不提供原始 JSON 文档。
func (rs *resultSet) Document() string { return "" }

func (rs *resultSet) Err() error { return rs.err }

func (rs *resultSet) NextResultSet() bool {
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

func (rs *resultSet) Affected() int64 { return 0 }

func (rs *resultSet) Messages() []domain.ServerMessage { return rs.messages }

func (rs *resultSet) Close() error {
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
	for _, marker := range []string{"BLOB", "BINARY", "BIT", "GEOMETRY", "POINT", "LINESTRING", "POLYGON"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
