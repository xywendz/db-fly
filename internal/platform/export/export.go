// Package export 提供结果集的 CSV / JSON / 文本表格输出，供 GUI 与 CLI 共用。
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/dbclient/dbclient/internal/domain"
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
