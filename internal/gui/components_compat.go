//go:build windows && cgo

package gui

import (
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/gui/components"
)

// 本文件是 gui 业务层到公共组件包的适配层：
// 与数据库类别无关的 UI 组件统一放在 components 包，业务代码继续用短名调用。
type editableCell = components.EditableCell

func newEditableCell() *editableCell { return components.NewEditableCell() }

const maxSampleRows = components.MaxSampleRows

func cellText(s string) string                 { return components.CellText(s) }
func newCellLabel() *widget.Label              { return components.NewCellLabel() }
func boolText(v bool) string                   { return components.BoolText(v) }
func columnRawType(c domain.ColumnInfo) string { return components.ColumnRawType(c) }

func sizeTableColumns(t *widget.Table, headers []string, sample [][]string) {
	components.SizeTableColumns(t, headers, sample)
}
func sizeSchemaColumns(t *widget.Table, headers []string, sample [][]string) {
	components.SizeSchemaColumns(t, headers, sample)
}
func parseColumnType(raw string, length int64) components.ColumnTypeParts {
	return components.ParseColumnType(raw, length)
}
func schemaColumnCell(c domain.ColumnInfo, col int) string {
	return components.SchemaColumnCell(c, col)
}
func columnSpecToInfo(c domain.ColumnSpec) domain.ColumnInfo {
	return components.ColumnSpecToInfo(c)
}
func buildSchemaComment(s domain.TableSchema) string {
	return components.BuildSchemaComment(s)
}

var schemaColumnHeaders = components.SchemaColumnHeaders
