// Package export 提供结果集的 CSV / JSON / 文本表格输出，供 GUI 与 CLI 共用。
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/db-fly/db-fly/internal/domain"
)

// CSV 输出单个结果页为 CSV，NULL 输出空字符串。
func CSV(w io.Writer, page domain.ResultPage) error {
	cw := csv.NewWriter(w)
	header := make([]string, len(page.Columns))
	for i, c := range page.Columns {
		header[i] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, row := range page.Rows {
		record := make([]string, len(row))
		for i, cell := range row {
			if cell.Null {
				record[i] = ""
			} else {
				record[i] = cell.Text
			}
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// JSON 输出完整查询结果。
func JSON(w io.Writer, outcome domain.QueryOutcome) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(outcome)
}

// SQL 把单个结果页写成 INSERT 语句。quote 用于引用标识符（表名/列名），
// 为空时用反引号；字符串字面量按标准 SQL 转义（单引号及其中的反斜杠）。
// 生成的语句不携带库名前缀，导入时由目标连接当前库决定。
func SQL(w io.Writer, table string, page domain.ResultPage, quote func(string) string) error {
	if quote == nil {
		quote = func(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
	}
	if len(page.Rows) == 0 {
		return nil
	}
	cols := make([]string, len(page.Columns))
	for i, c := range page.Columns {
		cols[i] = quote(c.Name)
	}
	colList := strings.Join(cols, ", ")
	for _, row := range page.Rows {
		vals := make([]string, len(row))
		for i, cell := range row {
			if cell.Null {
				vals[i] = "NULL"
			} else {
				vals[i] = sqlStringLiteral(cell.Text)
			}
		}
		if _, err := fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES (%s);\n",
			quote(table), colList, strings.Join(vals, ", ")); err != nil {
			return err
		}
	}
	return nil
}

// sqlStringLiteral 把字段值转成 SQL 字符串字面量：反斜杠与单引号各转义一次。
func sqlStringLiteral(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + s + "'"
}

// Table 输出对齐的文本表格，供 CLI 使用。
func Table(w io.Writer, page domain.ResultPage) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for i, c := range page.Columns {
		if i > 0 {
			_, _ = fmt.Fprint(tw, "\t")
		}
		_, _ = fmt.Fprint(tw, c.Name)
	}
	_, _ = fmt.Fprintln(tw)
	for _, row := range page.Rows {
		for i, cell := range row {
			if i > 0 {
				_, _ = fmt.Fprint(tw, "\t")
			}
			_, _ = fmt.Fprint(tw, cell.Display())
		}
		_, _ = fmt.Fprintln(tw)
	}
	return tw.Flush()
}
