package mssql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// batchResultSet 聚合多个批/多个结果集。
// NextResultSet 先推进当前批内部的后续结果集，再切换到下一批。
type batchResultSet struct {
	sets     []*sql.Rows
	index    int
	conn     *sql.Conn
	cols     []domain.Column
	binary   []bool
	current  domain.Row
	doc      string
	err      error
	affected int64
	messages []domain.ServerMessage
	cleanup  func(context.Context)
}

func newBatchResultSet(sets []*sql.Rows, conn *sql.Conn, affected int64,
	messages []domain.ServerMessage, cleanup func(context.Context)) (*batchResultSet, error) {
	rs := &batchResultSet{
		sets: sets, conn: conn, affected: affected, messages: messages, cleanup: cleanup,
	}
	if err := rs.refreshColumns(); err != nil {
		return nil, err
	}
	return rs, nil
}

func (rs *batchResultSet) active() *sql.Rows {
	if rs.index >= len(rs.sets) {
		return nil
	}
	return rs.sets[rs.index]
}

func (rs *batchResultSet) refreshColumns() error {
	rows := rs.active()
	if rows == nil {
		rs.cols, rs.binary = nil, nil
		return nil
	}
	types, err := rows.ColumnTypes()
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

func (rs *batchResultSet) Columns() []domain.Column {
	out := make([]domain.Column, len(rs.cols))
	copy(out, rs.cols)
	return out
}

func (rs *batchResultSet) Next() bool {
	rows := rs.active()
	if rows == nil {
		return false
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
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
	if err := rows.Scan(values...); err != nil {
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

func (rs *batchResultSet) Row() domain.Row { return rs.current }

func (rs *batchResultSet) Document() string { return rs.doc }

func (rs *batchResultSet) Err() error { return rs.err }

func (rs *batchResultSet) NextResultSet() bool {
	rows := rs.active()
	if rows == nil {
		return false
	}
	if rows.NextResultSet() {
		if err := rs.refreshColumns(); err != nil {
			rs.err = err
			return false
		}
		rs.current = nil
		return true
	}
	if rs.index+1 < len(rs.sets) {
		rs.index++
		if err := rs.refreshColumns(); err != nil {
			rs.err = err
			return false
		}
		rs.current = nil
		return true
	}
	return false
}

func (rs *batchResultSet) Affected() int64 { return rs.affected }

func (rs *batchResultSet) Messages() []domain.ServerMessage { return rs.messages }

func (rs *batchResultSet) Close() error {
	if rs.cleanup != nil {
		rs.cleanup(context.Background())
		rs.cleanup = nil
	}
	var firstErr error
	for _, rows := range rs.sets {
		if err := rows.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if rs.conn != nil {
		if err := rs.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func isBinaryType(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"BINARY", "IMAGE", "TIMESTAMP", "ROWVERSION"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
