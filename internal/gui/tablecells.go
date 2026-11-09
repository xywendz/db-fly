//go:build windows && cgo

package gui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

const (
	minColumnWidth = 120
	maxColumnWidth = 320
	cellCharWidth  = 8
	cellPadding    = 24
	maxSampleRows  = 50
	maxMeasureText = 48
)

var cellWhitespace = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ")

// newCellLabel 创建表格单元格模板。
// Fyne 的 Label 默认不截断（TextTruncateOff），widget.Table 未显式设置列宽时
// 取模板的 MinSize（空 Label 仅十几像素），长文本会溢出到相邻单元格造成重叠。
func newCellLabel() *widget.Label {
	label := widget.NewLabel("")
	label.Wrapping = fyne.TextWrapOff
	label.Truncation = fyne.TextTruncateEllipsis
	return label
}

// cellText 把单元格内容规范为单行，避免换行符撑高表格行。
func cellText(s string) string {
	return cellWhitespace.Replace(s)
}

// columnWidth 按内容估算列宽（中文/全角按双宽计），限制在可读区间。
func columnWidth(s string) float32 {
	runes := []rune(s)
	if len(runes) > maxMeasureText {
		runes = runes[:maxMeasureText]
	}
	var units float32
	for _, r := range runes {
		if r > 0x2e7f {
			units += 2
		} else {
			units++
		}
	}
	width := units*cellCharWidth + cellPadding
	if width < minColumnWidth {
		return minColumnWidth
	}
	if width > maxColumnWidth {
		return maxColumnWidth
	}
	return width
}

// sizeTableColumns 按表头和数据样本给每列设置宽度，避免默认窄列导致重叠。
func sizeTableColumns(table *widget.Table, headers []string, sample [][]string) {
	if len(sample) > maxSampleRows {
		sample = sample[:maxSampleRows]
	}
	for i, header := range headers {
		width := columnWidth(header)
		for _, row := range sample {
			if i < len(row) {
				if w := columnWidth(row[i]); w > width {
					width = w
				}
			}
		}
		table.SetColumnWidth(i, width)
	}
}
