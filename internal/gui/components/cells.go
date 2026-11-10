//go:build windows && cgo

// Package components 存放与数据库业务无关的可复用 Fyne UI 组件。
// gui 包按数据库类别装配业务视图，通用网格/标签等组件一律放在本包。
package components

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

const (
	MinColumnWidth = 120
	MaxColumnWidth = 320
	CellCharWidth  = 8
	CellPadding    = 24
	MaxSampleRows  = 50
	MaxMeasureText = 48
)

var CellWhitespace = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")

// CellText 把单元格内容规范为单行，避免换行符撑高表格行。
func CellText(s string) string { return CellWhitespace.Replace(s) }

// NewCellLabel 创建表格单元格模板。Fyne 的 Label 默认不截断，
// 长文本会溢出到相邻单元格，因此显式关闭自动换行并启用省略号。
func NewCellLabel() *widget.Label {
	label := widget.NewLabel("")
	label.Wrapping = fyne.TextWrapOff
	label.Truncation = fyne.TextTruncateEllipsis
	return label
}

// ColumnWidth 按内容估算列宽，中文/全角按双宽计。
func ColumnWidth(s string) float32 {
	runes := []rune(s)
	if len(runes) > MaxMeasureText {
		runes = runes[:MaxMeasureText]
	}
	var units float32
	for _, r := range runes {
		if r > 0x2e7f {
			units += 2
		} else {
			units++
		}
	}
	width := units*CellCharWidth + CellPadding
	if width < MinColumnWidth {
		return MinColumnWidth
	}
	if width > MaxColumnWidth {
		return MaxColumnWidth
	}
	return width
}

// SizeTableColumns 按表头和数据样本给每列设置宽度。
func SizeTableColumns(table *widget.Table, headers []string, sample [][]string) {
	if len(sample) > MaxSampleRows {
		sample = sample[:MaxSampleRows]
	}
	for i, header := range headers {
		width := ColumnWidth(header)
		for _, row := range sample {
			if i < len(row) {
				if w := ColumnWidth(row[i]); w > width {
					width = w
				}
			}
		}
		table.SetColumnWidth(i, width)
	}
}

// SchemaNarrowColumns 是字段网格中的窄列，避免所有列都按通用最小列宽铺开。
var SchemaNarrowColumns = map[int]float32{2: 76, 3: 76, 4: 72, 5: 72, 6: 88}

func SizeSchemaColumns(table *widget.Table, headers []string, sample [][]string) {
	SizeTableColumns(table, headers, sample)
	for col, width := range SchemaNarrowColumns {
		if col < len(headers) {
			table.SetColumnWidth(col, width)
		}
	}
}

func BoolText(v bool) string {
	if v {
		return "✓"
	}
	return ""
}
