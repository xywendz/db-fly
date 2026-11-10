//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
)

func effectiveColumnType(c domain.ColumnInfo) string {
	if c.RawType != "" {
		return c.RawType
	}
	return c.DataType
}

// columnSpecFromInfo 把元数据还原为结构化字段定义，供重命名字段等需要完整定义的动作使用。
func columnSpecFromInfo(c domain.ColumnInfo) domain.ColumnSpec {
	spec := domain.ColumnSpec{
		Name: strings.TrimSpace(c.Name), Type: effectiveColumnType(c),
		Nullable: c.Nullable, Comment: c.Comment,
		PrimaryKey: c.PrimaryKey, AutoIncrement: c.AutoIncrement,
	}
	if c.HasDefault && c.Default != "" {
		value := c.Default
		spec.Default = &value
	}
	return spec
}

func (v *schemaView) selectedColumn() (domain.ColumnInfo, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.hasColSel || v.colSel.Row <= 0 || v.colSel.Row-1 >= len(v.columns) {
		return domain.ColumnInfo{}, false
	}
	return v.columns[v.colSel.Row-1], true
}

func (v *schemaView) selectedIndex() (domain.IndexInfo, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.hasIdxSel || v.idxSel.Row <= 0 || v.idxSel.Row-1 >= len(v.indexes) {
		return domain.IndexInfo{}, false
	}
	return v.indexes[v.idxSel.Row-1], true
}

func (v *schemaView) columnNames() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	names := make([]string, 0, len(v.columns))
	for _, c := range v.columns {
		names = append(names, c.Name)
	}
	return names
}

func (v *schemaView) target() (string, string, string) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.conn, v.database, v.table
}

func (v *schemaView) requireEditableTable() bool {
	conn, _, table := v.target()
	if conn == "" || table == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return false
	}
	if !v.caps.Has(domain.CapAlterTable) {
		v.u.showInfo("提示", "当前连接的模块未声明修改表结构能力")
		return false
	}
	return true
}

// ensureCurrent 右键触发的动作可能发生在结构尚未加载完成时，先补一次 Describe。
func (v *schemaView) ensureCurrent(open func()) {
	conn, database, table := v.target()
	if table == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	v.mu.RLock()
	current := v.current
	v.mu.RUnlock()
	if current.Ref.Name == table && current.Ref.Database == database {
		open()
		return
	}
	v.u.setStatus("正在读取 %s 的最新结构 …", table)
	v.u.async(func() {
		schema, err := v.u.core.Describe(context.Background(), conn,
			domain.ObjectRef{Database: database, Name: table})
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() {
			v.mu.Lock()
			v.current = schema
			v.columns, v.indexes, v.foreign = schema.Columns, schema.Indexes, schema.ForeignKeys
			v.hasColSel, v.hasIdxSel = false, false
			v.mu.Unlock()
			v.sizeColumnsToData()
			v.colTable.Refresh()
			v.idxTable.Refresh()
			v.fkTable.Refresh()
			v.updateSelectionHints()
			open()
		})
	})
}

func (v *schemaView) previewChanges(title string, changes []domain.SchemaChange) {
	conn, database, table := v.target()
	ref := domain.ObjectRef{Database: database, Name: table}
	v.u.async(func() {
		script, err := v.u.core.PreviewAlterTable(context.Background(), conn, ref, changes)
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() { v.u.showScriptDialog(title+" 预览", conn, script) })
	})
}

// ---------- 字段操作 ----------

func (v *schemaView) showAddColumn() { v.openAddColumn("") }

// showInsertColumn 参考 Navicat 的 Insert Field：在选中字段之后插入新字段。
func (v *schemaView) showInsertColumn() {
	if !v.requireEditableTable() {
		return
	}
	col, ok := v.selectedColumn()
	if !ok {
		v.u.showInfo("提示", "请先在字段网格中选中一个字段，新字段将插入到它之后")
		return
	}
	v.openAddColumn(col.Name)
}

func (v *schemaView) openAddColumn(afterName string) {
	if !v.requireEditableTable() {
		return
	}
	_, _, table := v.target()
	name := widget.NewEntry()
	name.SetPlaceHolder("字段名，如 age")
	typ := widget.NewEntry()
	typ.SetText("VARCHAR(255)")
	nullable := widget.NewCheck("允许 NULL", nil)
	nullable.SetChecked(true)
	def := widget.NewEntry()
	def.SetPlaceHolder("留空表示无默认值")
	comment := widget.NewEntry()
	comment.SetPlaceHolder("字段注释（可选）")
	after := widget.NewSelect(v.columnNames(), nil)
	if afterName != "" {
		after.SetSelected(afterName)
	}

	title := "新增字段 · " + table
	if afterName != "" {
		title = "插入字段（在 " + afterName + " 之后）· " + table
	}
	dialog.ShowForm(title, "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("字段名", name),
		widget.NewFormItem("类型", typ),
		widget.NewFormItem("", nullable),
		widget.NewFormItem("默认值", def),
		widget.NewFormItem("注释", comment),
		widget.NewFormItem("位于某字段之后", after),
	}, func(ok bool) {
		if !ok {
			return
		}
		spec := domain.ColumnSpec{
			Name: strings.TrimSpace(name.Text), Type: strings.TrimSpace(typ.Text),
			Nullable: nullable.Checked, Comment: comment.Text, After: after.Selected,
		}
		if spec.Name == "" || spec.Type == "" {
			v.u.showInfo("输入有误", "字段名与类型不能为空")
			return
		}
		if def.Text != "" {
			value := def.Text
			spec.Default = &value
		}
		v.previewChanges("ADD COLUMN", []domain.SchemaChange{
			{Kind: domain.ChangeAddColumn, Column: spec},
		})
	}, v.u.win)
}

func (v *schemaView) showModifyColumn() {
	if !v.requireEditableTable() {
		return
	}
	col, ok := v.selectedColumn()
	if !ok {
		v.u.showInfo("提示", "请先在字段页签中单击选中一个字段")
		return
	}
	typ := widget.NewEntry()
	typ.SetText(effectiveColumnType(col))
	nullable := widget.NewCheck("允许 NULL", nil)
	nullable.SetChecked(col.Nullable)
	def := widget.NewEntry()
	if col.HasDefault {
		def.SetText(col.Default)
	}
	def.SetPlaceHolder("留空表示无默认值")
	comment := widget.NewEntry()
	comment.SetText(col.Comment)

	dialog.ShowForm("修改字段类型 · "+col.Name, "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("原字段", widget.NewLabel(col.Name)),
		widget.NewFormItem("新类型", typ),
		widget.NewFormItem("", nullable),
		widget.NewFormItem("默认值", def),
		widget.NewFormItem("注释", comment),
	}, func(ok bool) {
		if !ok {
			return
		}
		if strings.TrimSpace(typ.Text) == "" {
			v.u.showInfo("输入有误", "字段类型不能为空")
			return
		}
		spec := columnSpecFromInfo(col)
		spec.Type = strings.TrimSpace(typ.Text)
		spec.Nullable = nullable.Checked
		spec.Comment = comment.Text
		spec.Default = nil
		if def.Text != "" {
			value := def.Text
			spec.Default = &value
		}
		v.previewChanges("MODIFY COLUMN", []domain.SchemaChange{
			{Kind: domain.ChangeModifyColumn, Column: spec},
		})
	}, v.u.win)
}

func (v *schemaView) showRenameColumn() {
	if !v.requireEditableTable() {
		return
	}
	col, ok := v.selectedColumn()
	if !ok {
		v.u.showInfo("提示", "请先在字段页签中单击选中一个字段")
		return
	}
	name := widget.NewEntry()
	name.SetText(col.Name)
	dialog.ShowForm("修改字段名称 · "+col.Name, "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("原字段名", widget.NewLabel(col.Name)),
		widget.NewFormItem("新字段名", name),
	}, func(ok bool) {
		if !ok {
			return
		}
		newName := strings.TrimSpace(name.Text)
		if newName == "" || newName == col.Name {
			v.u.showInfo("输入有误", "请填写一个不同的新字段名")
			return
		}
		spec := columnSpecFromInfo(col)
		spec.Name = newName
		v.previewChanges("RENAME COLUMN", []domain.SchemaChange{
			{Kind: domain.ChangeRenameColumn, OldName: col.Name, Column: spec},
		})
	}, v.u.win)
}

func (v *schemaView) showDropColumn() {
	if !v.requireEditableTable() {
		return
	}
	col, ok := v.selectedColumn()
	if !ok {
		v.u.showInfo("提示", "请先在字段页签中单击选中一个字段")
		return
	}
	dialog.ShowConfirm("删除字段",
		fmt.Sprintf("即将生成删除字段 %s 的脚本，该列全部数据将丢失且不可自动回滚。是否继续？", col.Name),
		func(ok bool) {
			if !ok {
				return
			}
			v.previewChanges("DROP COLUMN", []domain.SchemaChange{
				{Kind: domain.ChangeDropColumn, Column: domain.ColumnSpec{Name: col.Name}},
			})
		}, v.u.win)
}

// ---------- 索引操作 ----------

func (v *schemaView) showAddIndex() {
	if !v.requireEditableTable() {
		return
	}
	name := widget.NewEntry()
	name.SetPlaceHolder("索引名，如 idx_name")
	cols := widget.NewEntry()
	cols.SetPlaceHolder("多个字段用英文逗号分隔，如 name, age")
	unique := widget.NewCheck("唯一索引", nil)
	columnSelect := widget.NewSelect(v.columnNames(), func(selected string) {
		if selected == "" {
			return
		}
		current := strings.TrimSpace(cols.Text)
		if current == "" {
			cols.SetText(selected)
		} else {
			cols.SetText(current + ", " + selected)
		}
	})

	dialog.ShowForm("新增索引", "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("索引名", name),
		widget.NewFormItem("字段列表", cols),
		widget.NewFormItem("从字段中追加", columnSelect),
		widget.NewFormItem("", unique),
	}, func(ok bool) {
		if !ok {
			return
		}
		indexName := strings.TrimSpace(name.Text)
		parts := strings.Split(cols.Text, ",")
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				cleaned = append(cleaned, s)
			}
		}
		if indexName == "" || len(cleaned) == 0 {
			v.u.showInfo("输入有误", "索引名与至少一个字段不能为空")
			return
		}
		v.previewChanges("ADD INDEX", []domain.SchemaChange{
			{Kind: domain.ChangeAddIndex, Index: domain.IndexSpec{
				Name: indexName, Columns: cleaned, Unique: unique.Checked,
			}},
		})
	}, v.u.win)
}

func (v *schemaView) showDropIndex() {
	if !v.requireEditableTable() {
		return
	}
	idx, ok := v.selectedIndex()
	if !ok {
		v.u.showInfo("提示", "请先在索引页签中单击选中一个索引")
		return
	}
	dialog.ShowConfirm("删除索引",
		fmt.Sprintf("即将生成删除索引 %s 的脚本，是否继续？", idx.Name), func(ok bool) {
			if !ok {
				return
			}
			v.previewChanges("DROP INDEX", []domain.SchemaChange{
				{Kind: domain.ChangeDropIndex, IndexName: idx.Name},
			})
		}, v.u.win)
}

// ---------- 表级操作 ----------

func (v *schemaView) showTableOptions() {
	if !v.requireEditableTable() {
		return
	}
	engine := widget.NewEntry()
	engine.SetPlaceHolder("存储引擎，如 InnoDB（不支持的方言会忽略）")
	charset := widget.NewEntry()
	charset.SetPlaceHolder("字符集，如 utf8mb4")
	collation := widget.NewEntry()
	collation.SetPlaceHolder("排序规则")
	comment := widget.NewEntry()
	comment.SetPlaceHolder("表注释")
	dialog.ShowForm("表选项", "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("存储引擎", engine),
		widget.NewFormItem("字符集", charset),
		widget.NewFormItem("排序规则", collation),
		widget.NewFormItem("表注释", comment),
	}, func(ok bool) {
		if !ok {
			return
		}
		change := domain.SchemaChange{
			Kind: domain.ChangeTableOptions, Engine: engine.Text,
			Charset: charset.Text, Collation: collation.Text, Comment: comment.Text,
		}
		if engine.Text == "" && charset.Text == "" && collation.Text == "" && comment.Text == "" {
			v.u.showInfo("提示", "没有填写任何表选项")
			return
		}
		v.previewChanges("TABLE OPTIONS", []domain.SchemaChange{change})
	}, v.u.win)
}

func (v *schemaView) showRenameTable() {
	conn, database, table := v.target()
	if conn == "" || table == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	name := widget.NewEntry()
	name.SetText(table)
	dialog.ShowForm("重命名表", "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("原表名", widget.NewLabel(database+"."+table)),
		widget.NewFormItem("新表名", name),
	}, func(ok bool) {
		if !ok {
			return
		}
		newName := strings.TrimSpace(name.Text)
		if newName == "" || newName == table {
			v.u.showInfo("输入有误", "请填写一个不同的新表名")
			return
		}
		ref := domain.ObjectRef{Database: database, Name: table}
		v.u.async(func() {
			script, err := v.u.core.PreviewRenameTable(context.Background(), conn, ref, newName)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialogWith("RENAME TABLE 预览", conn, script, false) })
		})
	}, v.u.win)
}
