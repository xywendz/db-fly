package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/db-fly/db-fly/internal/domain"
)

// mongoResultSet 先采样若干文档计算列式投影，再流式返回剩余文档。
// 每行同时保留原始 JSON 文档，供 JSON 树/文档视图使用。
type mongoResultSet struct {
	ctx         context.Context
	cursor      *mongo.Cursor
	cols        []domain.Column
	paths       []string
	pending     []bson.M
	pendingDocs []string
	current     domain.Row
	currentDoc  string
	err         error
}

func newMongoResultSet(ctx context.Context, cursor *mongo.Cursor) (*mongoResultSet, error) {
	rs := &mongoResultSet{ctx: ctx, cursor: cursor}
	seen := make(map[string]bool)
	for i := 0; i < sampleDocuments && cursor.Next(ctx); i++ {
		jsonText, doc, err := rawToDocument(cursor.Current)
		if err != nil {
			rs.err = err
			return rs, err
		}
		rs.pending = append(rs.pending, doc)
		rs.pendingDocs = append(rs.pendingDocs, jsonText)
		flattenPaths(doc, "", 0, &rs.paths, seen)
	}
	if err := cursor.Err(); err != nil {
		rs.err = mapQueryError(err)
		return rs, rs.err
	}
	rs.cols = make([]domain.Column, len(rs.paths))
	for i, path := range rs.paths {
		rs.cols[i] = domain.Column{Name: path, Type: "bson"}
	}
	return rs, nil
}

// newSingleDocResultSet 把单个命令结果（如 explain）包装成结果集。
func newSingleDocResultSet(raw bson.Raw) (domain.ResultSet, error) {
	rs := &mongoResultSet{cols: []domain.Column{{Name: "document", Type: "json"}}}
	jsonText, doc, err := rawToDocument(raw)
	if err != nil {
		return nil, err
	}
	rs.pending = append(rs.pending, doc)
	rs.pendingDocs = append(rs.pendingDocs, jsonText)
	rs.paths = []string{"__document__"}
	return rs, nil
}

func (rs *mongoResultSet) Columns() []domain.Column {
	out := make([]domain.Column, len(rs.cols))
	copy(out, rs.cols)
	return out
}

func (rs *mongoResultSet) Next() bool {
	var (
		doc      bson.M
		jsonText string
	)
	if len(rs.pending) > 0 {
		doc, jsonText = rs.pending[0], rs.pendingDocs[0]
		rs.pending, rs.pendingDocs = rs.pending[1:], rs.pendingDocs[1:]
	} else {
		if rs.cursor == nil || !rs.cursor.Next(rs.ctx) {
			if rs.cursor != nil {
				if err := rs.cursor.Err(); err != nil {
					rs.err = mapQueryError(err)
				}
			}
			return false
		}
		var err error
		jsonText, doc, err = rawToDocument(rs.cursor.Current)
		if err != nil {
			rs.err = err
			return false
		}
	}
	rs.currentDoc = jsonText

	if len(rs.paths) == 1 && rs.paths[0] == "__document__" {
		rs.current = domain.Row{domain.Cell{Kind: domain.ValueJSON, Text: truncate(jsonText)}}
		return true
	}
	row := make(domain.Row, len(rs.paths))
	for i, path := range rs.paths {
		value, ok := lookupPath(doc, path)
		if !ok || value == nil {
			row[i] = domain.NewNullCell()
			continue
		}
		row[i] = cellValue(value)
	}
	rs.current = row
	return true
}

func (rs *mongoResultSet) Row() domain.Row { return rs.current }

func (rs *mongoResultSet) Document() string { return rs.currentDoc }

func (rs *mongoResultSet) Err() error { return rs.err }

// MongoDB 查询只产生单一结果集。
func (rs *mongoResultSet) NextResultSet() bool { return false }

func (rs *mongoResultSet) Affected() int64 { return 0 }

func (rs *mongoResultSet) Messages() []domain.ServerMessage { return nil }

func (rs *mongoResultSet) Close() error {
	if rs.cursor != nil {
		return rs.cursor.Close(rs.ctx)
	}
	return nil
}
