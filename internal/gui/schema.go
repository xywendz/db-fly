//go:build windows && cgo

package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
)

var (
	schemaColumnHeaders = []string{"字段", "类型", "可空", "默认值", "主键", "自增", "注释"}
	schemaIndexHeaders  = []string{"索引名", "列", "唯一", "类型"}
)

type schemaView struct {
	u *ui

	mu       sync.RWMutex
	seq      int
	conn     string
	database string
	table    string
	columns  []domain.ColumnInfo
	indexes  []domain.IndexInfo
	current  domain.TableSchema

	info     *widget.Label
	colTable *widget.Table
	idxTable *widget.Table
	ddl      *widget.Entry
}

func newSchemaView(u *ui) *schemaView { return &schemaView{u: u} }

func (v *schemaView) build() fyne.CanvasObject {
	v.info = widget.NewLabel("请在左侧选择一张表")
	v.colTable = widget.NewTable(
		func() (int, int) {
			v.mu.RLock()
			defer v.mu.RUnlock()
			return len(v.columns) + 1, len(schemaColumnHeaders)
		},
		func() fyne.CanvasObject { return newCellLabel() },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(cellText(v.columnCell(id)))
		},
	)
	v.idxTable = widget.NewTable(
		func() (int, int) {
			v.mu.RLock()
			defer v.mu.RUnlock()
			return len(v.indexes) + 1, len(schemaIndexHeaders)
		},
		func() fyne.CanvasObject { return newCellLabel() },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(cellText(v.indexCell(id)))
		},
	)
	sizeTableColumns(v.colTable, schemaColumnHeaders, nil)
	sizeTableColumns(v.idxTable, schemaIndexHeaders, nil)

	v.ddl = widget.NewMultiLineEntry()
	v.ddl.Wrapping = fyne.TextWrapOff

	tables := container.NewVSplit(v.colTable, v.idxTable)
	tables.Offset = 0.65
	tabs := container.NewAppTabs(
		container.NewTabItem("字段 / 索引", tables),
		container.NewTabItem("建表 DDL", v.ddl),
	)
	toolbar := container.NewHBox(
		widget.NewButton("刷新", v.reload),
		widget.NewButton("查看建表 SQL", v.showDDL),
		widget.NewButton("修改结构（预览 SQL）", v.showAlterForm),
		widget.NewButton("新建表（预览 SQL）", v.showCreateTable),
		widget.NewButton("删除表（预览 SQL）", v.showDropTable),
	)
	return container.NewBorder(toolbar, v.info, nil, nil, tabs)
}

func (v *schemaView) columnCell(id widget.TableCellID) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	headers := schemaColumnHeaders
	if id.Row == 0 {
		if id.Col < len(headers) {
			return headers[id.Col]
		}
		return ""
	}
	if id.Row-1 >= len(v.columns) {
		return ""
	}
	c := v.columns[id.Row-1]
	switch id.Col {
	case 0:
		return c.Name
	case 1:
		return c.RawType
	case 2:
		return fmt.Sprintf("%v", c.Nullable)
	case 3:
		if !c.HasDefault {
			return "-"
		}
		return c.Default
	case 4:
		return fmt.Sprintf("%v", c.PrimaryKey)
	case 5:
		return fmt.Sprintf("%v", c.AutoIncrement)
	case 6:
		return c.Comment
	}
	return ""
}

func (v *schemaView) indexCell(id widget.TableCellID) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	headers := schemaIndexHeaders
	if id.Row == 0 {
		if id.Col < len(headers) {
			return headers[id.Col]
		}
		return ""
	}
	if id.Row-1 >= len(v.indexes) {
		return ""
	}
	idx := v.indexes[id.Row-1]
	switch id.Col {
	case 0:
		return idx.Name
	case 1:
		return strings.Join(idx.Columns, ", ")
	case 2:
		return fmt.Sprintf("%v", idx.Unique)
	case 3:
		return idx.IndexType
	}
	return ""
}

func (v *schemaView) load(conn, database, table string) {
	v.mu.Lock()
	v.conn, v.database, v.table = conn, database, table
	v.seq++
	seq := v.seq
	v.mu.Unlock()

	v.u.setStatus("正在加载 %s 的结构 …", table)
	v.u.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		schema, err := v.u.core.Describe(ctx, conn, domain.ObjectRef{Database: database, Name: table})
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() { v.render(seq, schema) })
	})
}

func (v *schemaView) reload() {
	v.mu.RLock()
	conn, database, table := v.conn, v.database, v.table
	v.mu.RUnlock()
	if table == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	v.load(conn, database, table)
}

func (v *schemaView) render(seq int, schema domain.TableSchema) {
	v.mu.Lock()
	if seq != v.seq {
		v.mu.Unlock()
		return
	}
	v.current = schema
	v.columns = schema.Columns
	v.indexes = schema.Indexes
	v.mu.Unlock()

	v.info.SetText(fmt.Sprintf("%s · 引擎 %s · 字符集 %s · %d 字段 / %d 索引",
		schema.Ref.String(), schema.Engine, schema.Charset, len(schema.Columns), len(schema.Indexes)))
	ddl := strings.TrimSpace(schema.DDL)
	switch {
	case ddl == "":
		ddl = "-- 该对象没有可展示的建表语句"
	case strings.HasSuffix(ddl, ";") || strings.HasPrefix(ddl, "#"):
		// 已包含语句结束符，或是命令式脚本，保持原文
	default:
		ddl += ";"
	}
	v.ddl.SetText(ddl)
	v.sizeColumnsToData()
	v.colTable.Refresh()
	v.idxTable.Refresh()
	v.u.setStatus("结构加载完成：%s", schema.Ref.String())
}

// sizeColumnsToData 按最新的字段/索引内容重新计算列宽，避免长文本互相覆盖。
func (v *schemaView) sizeColumnsToData() {
	v.mu.RLock()
	colSample := make([][]string, 0, len(v.columns))
	for _, c := range v.columns {
		colSample = append(colSample, []string{c.Name, c.RawType, "", c.Default, "", "", c.Comment})
	}
	idxSample := make([][]string, 0, len(v.indexes))
	for _, idx := range v.indexes {
		idxSample = append(idxSample, []string{idx.Name, strings.Join(idx.Columns, ", "), "", idx.IndexType})
	}
	v.mu.RUnlock()

	sizeTableColumns(v.colTable, schemaColumnHeaders, colSample)
	sizeTableColumns(v.idxTable, schemaIndexHeaders, idxSample)
}

// showDDL 展示服务端建表语句（或由元数据反向渲染 / 等价创建命令）。
func (v *schemaView) showDDL() {
	if v.conn == "" || v.table == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	conn := v.conn
	ref := domain.ObjectRef{Database: v.database, Name: v.table}
	v.u.async(func() {
		ddl, err := v.u.core.ShowCreate(context.Background(), conn, ref)
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() {
			content := widget.NewMultiLineEntry()
			content.Wrapping = fyne.TextWrapOff
			content.SetText(ddl)
			scroll := container.NewScroll(content)
			scroll.SetMinSize(fyne.NewSize(760, 460))
			dialog.ShowCustom("建表语句 · "+ref.String(), "关闭", scroll, v.u.win)
		})
	})
}

func (v *schemaView) showAlterForm() {
	v.mu.RLock()
	conn, database, table, schema := v.conn, v.database, v.table, v.current
	v.mu.RUnlock()
	if table == "" {
		v.u.showInfo("提示", "请先选择一张表")
		return
	}

	colName := widget.NewEntry()
	colName.SetPlaceHolder("字段名，如 age")
	colType := widget.NewEntry()
	colType.SetText("VARCHAR(255)")
	colNullable := widget.NewCheck("允许 NULL", nil)
	colNullable.SetChecked(true)
	colDefault := widget.NewEntry()
	colComment := widget.NewEntry()

	idxName := widget.NewEntry()
	idxCols := widget.NewEntry()
	idxCols.SetPlaceHolder("多个字段用英文逗号分隔")
	idxUnique := widget.NewCheck("唯一索引", nil)

	dropNames := make([]string, 0, len(schema.Columns))
	for _, c := range schema.Columns {
		dropNames = append(dropNames, c.Name)
	}
	dropCol := widget.NewSelect(dropNames, nil)

	engine := widget.NewEntry()
	charset := widget.NewEntry()
	tableComment := widget.NewEntry()

	items := []*widget.FormItem{
		widget.NewFormItem("新增字段", colName),
		widget.NewFormItem("字段类型", colType),
		widget.NewFormItem("", colNullable),
		widget.NewFormItem("默认值(留空为无)", colDefault),
		widget.NewFormItem("字段注释", colComment),
		widget.NewFormItem("新增索引名", idxName),
		widget.NewFormItem("索引字段", idxCols),
		widget.NewFormItem("", idxUnique),
		widget.NewFormItem("删除字段", dropCol),
		widget.NewFormItem("修改存储引擎", engine),
		widget.NewFormItem("修改字符集", charset),
		widget.NewFormItem("修改表注释", tableComment),
	}
	dialog.ShowForm("修改表结构", "生成脚本", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		changes := make([]domain.SchemaChange, 0, 4)
		if strings.TrimSpace(colName.Text) != "" {
			spec := domain.ColumnSpec{
				Name: colName.Text, Type: colType.Text, Nullable: colNullable.Checked,
				Comment: colComment.Text,
			}
			if colDefault.Text != "" {
				value := colDefault.Text
				spec.Default = &value
			}
			changes = append(changes, domain.SchemaChange{
				Kind: domain.ChangeAddColumn, Column: spec,
			})
		}
		if strings.TrimSpace(idxName.Text) != "" && strings.TrimSpace(idxCols.Text) != "" {
			cols := strings.Split(idxCols.Text, ",")
			for i := range cols {
				cols[i] = strings.TrimSpace(cols[i])
			}
			changes = append(changes, domain.SchemaChange{
				Kind: domain.ChangeAddIndex,
				Index: domain.IndexSpec{
					Name: idxName.Text, Columns: cols, Unique: idxUnique.Checked,
				},
			})
		}
		if dropCol.Selected != "" {
			changes = append(changes, domain.SchemaChange{
				Kind:   domain.ChangeDropColumn,
				Column: domain.ColumnSpec{Name: dropCol.Selected},
			})
		}
		if engine.Text != "" || charset.Text != "" || tableComment.Text != "" {
			changes = append(changes, domain.SchemaChange{
				Kind: domain.ChangeTableOptions, Engine: engine.Text,
				Charset: charset.Text, Comment: tableComment.Text,
			})
		}
		if len(changes) == 0 {
			v.u.showInfo("提示", "没有填写任何变更")
			return
		}
		ref := domain.ObjectRef{Database: database, Name: table}
		v.u.async(func() {
			script, err := v.u.core.PreviewAlterTable(context.Background(), conn, ref, changes)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialog("ALTER TABLE 预览", conn, script) })
		})
	}, v.u.win)
}

func (v *schemaView) showCreateTable() {
	v.mu.RLock()
	conn, database := v.conn, v.database
	v.mu.RUnlock()
	if conn == "" || database == "" {
		v.u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	name := widget.NewEntry()
	name.SetText("new_table")
	definition := widget.NewMultiLineEntry()
	definition.Wrapping = fyne.TextWrapOff
	definition.SetText(`{
  "columns": [
    {"name": "id", "type": "BIGINT", "nullable": false, "auto_increment": true, "primary_key": true},
    {"name": "name", "type": "VARCHAR(128)", "nullable": false},
    {"name": "created_at", "type": "DATETIME", "nullable": true}
  ],
  "indexes": [
    {"name": "uk_name", "columns": ["name"], "unique": true}
  ],
  "engine": "InnoDB",
  "charset": "utf8mb4",
  "comment": "由 db-fly 创建",
  "if_not_exists": true
}`)

	content := container.NewBorder(
		widget.NewForm(widget.NewFormItem("表名", name)),
		nil, nil, nil,
		container.NewScroll(definition),
	)
	var d *dialog.CustomDialog
	generateBtn := widget.NewButton("生成脚本", func() {
		var def domain.TableDefinition
		if err := json.Unmarshal([]byte(definition.Text), &def); err != nil {
			v.u.showInfo("表定义有误", err.Error())
			return
		}
		def.Ref = domain.ObjectRef{Database: database, Name: name.Text}
		d.Hide()
		v.u.async(func() {
			script, err := v.u.core.PreviewCreateTable(context.Background(), conn, def)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialog("CREATE TABLE 预览", conn, script) })
		})
	})
	cancelBtn := widget.NewButton("取消", func() { d.Hide() })
	d = dialog.NewCustom("新建表（可编辑 JSON 表定义）", "关闭",
		container.NewBorder(nil, container.NewHBox(cancelBtn, generateBtn), nil, nil, content), v.u.win)
	d.Resize(fyne.NewSize(700, 520))
	d.Show()
}

func (v *schemaView) showDropTable() {
	v.mu.RLock()
	conn, database, table := v.conn, v.database, v.table
	v.mu.RUnlock()
	if table == "" {
		v.u.showInfo("提示", "请先选择一张表")
		return
	}
	ref := domain.ObjectRef{Database: database, Name: table}
	dialog.ShowConfirm("删除表", "即将生成删除表 "+ref.String()+" 的脚本，数据不可恢复。是否继续？", func(ok bool) {
		if !ok {
			return
		}
		v.u.async(func() {
			script, err := v.u.core.PreviewDropObject(context.Background(), conn, ref)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialog("DROP TABLE 预览", conn, script) })
		})
	}, v.u.win)
}
