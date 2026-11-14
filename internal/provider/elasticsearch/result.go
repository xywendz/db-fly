package elasticsearch

import (
	"fmt"
	"sort"

	"github.com/db-fly/db-fly/internal/domain"
)

type esPage struct {
	columns []domain.Column
	rows    []domain.Row
	docs    []string
}

// esResultSet 把一次 _search 响应投影为「命中文档」+「聚合桶」两个结果集。
// 每行同时保留原始 JSON 文档，供 JSON 树视图与 NDJSON 导出使用。
type esResultSet struct {
	pages    []esPage
	page     int
	pos      int
	messages []domain.ServerMessage
	err      error
}

func newESResultSet(resp map[string]any, index string) (domain.ResultSet, error) {
	hits := asMap(resp["hits"])
	hitList := toSlice(hits["hits"])
	docs := make([]map[string]any, 0, len(hitList))
	docTexts := make([]string, 0, len(hitList))
	for _, hit := range hitList {
		text, doc, err := documentJSON(asMap(hit))
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
		docTexts = append(docTexts, text)
	}
	paths := []string{"_id"}
	seen := map[string]bool{"_id": true}
	for i := 0; i < len(docs) && i < sampleDocuments; i++ {
		flattenPaths(docs[i], "", 0, &paths, seen)
	}
	columns := make([]domain.Column, len(paths))
	for i, path := range paths {
		columns[i] = domain.Column{Name: path, Type: "json"}
	}
	rows := make([]domain.Row, len(docs))
	for i, doc := range docs {
		rows[i] = projectRow(doc, paths)
	}
	pages := []esPage{{columns: columns, rows: rows, docs: docTexts}}
	if aggs := asMap(resp["aggregations"]); len(aggs) > 0 {
		pages = append(pages, aggregationPage(aggs))
	}
	messages := []domain.ServerMessage{
		{Level: "info", Text: fmt.Sprintf("目标索引 %s · 命中 %v · took %v ms", index, hits["total"], resp["took"])},
	}
	if timedOut, ok := resp["timed_out"].(bool); ok && timedOut {
		messages = append(messages, domain.ServerMessage{Level: "warn", Text: "查询在部分分片上超时，结果可能不完整"})
	}
	if shards := asMap(resp["_shards"]); len(shards) > 0 {
		messages = append(messages, domain.ServerMessage{
			Level: "info",
			Text:  fmt.Sprintf("分片 successful=%v failed=%v skipped=%v", shards["successful"], shards["failed"], shards["skipped"]),
		})
	}
	return &esResultSet{pages: pages, messages: messages}, nil
}

func projectRow(doc map[string]any, paths []string) domain.Row {
	row := make(domain.Row, len(paths))
	for i, path := range paths {
		value, ok := lookupPath(doc, path)
		if !ok || value == nil {
			row[i] = domain.NewNullCell()
			continue
		}
		row[i] = cellValue(value)
	}
	return row
}

func aggregationPage(aggs map[string]any) esPage {
	columns := []domain.Column{
		{Name: "aggregation", Type: "text"},
		{Name: "bucket", Type: "text"},
		{Name: "value", Type: "text"},
	}
	names := make([]string, 0, len(aggs))
	for name := range aggs {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]domain.Row, 0, len(names))
	for _, name := range names {
		detail := asMap(aggs[name])
		if buckets := toSlice(detail["buckets"]); len(buckets) > 0 {
			for _, bucket := range buckets {
				b := asMap(bucket)
				rows = append(rows, domain.Row{
					domain.NewTextCell(name),
					domain.NewTextCell(fmt.Sprint(b["key"])),
					domain.NewTextCell(fmt.Sprint(b["doc_count"])),
				})
			}
			continue
		}
		rows = append(rows, domain.Row{
			domain.NewTextCell(name),
			domain.NewTextCell(""),
			domain.NewTextCell(fmt.Sprint(detail["value"])),
		})
	}
	return esPage{columns: columns, rows: rows}
}

func newSingleDocResultSet(text string) domain.ResultSet {
	return &esResultSet{pages: []esPage{{
		columns: []domain.Column{{Name: "document", Type: "json"}},
		rows:    []domain.Row{{domain.Cell{Kind: domain.ValueJSON, Text: truncate(text)}}},
		docs:    []string{text},
	}}}
}

func (rs *esResultSet) Columns() []domain.Column {
	if rs.page >= len(rs.pages) {
		return []domain.Column{}
	}
	out := make([]domain.Column, len(rs.pages[rs.page].columns))
	copy(out, rs.pages[rs.page].columns)
	return out
}

func (rs *esResultSet) Next() bool {
	if rs.err != nil || rs.page >= len(rs.pages) {
		return false
	}
	if rs.pos >= len(rs.pages[rs.page].rows) {
		return false
	}
	rs.pos++
	return true
}

func (rs *esResultSet) Row() domain.Row {
	if rs.pos == 0 || rs.page >= len(rs.pages) || rs.pos-1 >= len(rs.pages[rs.page].rows) {
		return nil
	}
	src := rs.pages[rs.page].rows[rs.pos-1]
	out := make(domain.Row, len(src))
	copy(out, src)
	return out
}

func (rs *esResultSet) Document() string {
	if rs.pos == 0 || rs.page >= len(rs.pages) || rs.pos-1 >= len(rs.pages[rs.page].docs) {
		return ""
	}
	return rs.pages[rs.page].docs[rs.pos-1]
}

func (rs *esResultSet) Err() error { return rs.err }

func (rs *esResultSet) NextResultSet() bool {
	if rs.page+1 >= len(rs.pages) {
		return false
	}
	rs.page++
	rs.pos = 0
	return true
}

func (rs *esResultSet) Affected() int64 { return 0 }

func (rs *esResultSet) Messages() []domain.ServerMessage {
	out := make([]domain.ServerMessage, len(rs.messages))
	copy(out, rs.messages)
	return out
}

func (rs *esResultSet) Close() error { return nil }
