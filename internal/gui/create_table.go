//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
)

var (
	// 与表结构页 schemaColumnHeaders 保持一致，让新建表与查看表结构视觉统一。
	designerColumnHeaders = schemaColumnHeaders
	designerIndexHeaders  = []string{"索引名", "字段", "唯一"}
)

// tableDesigner 是「新建表」的表结构编辑器：字段 / 索引 / 表选项三个页签，
// 用网格 + 属性表单逐行录入，最终仍生成 domain.TableDefinition 交给后端生成方言脚本。
type tableDesigner struct {
	u         *ui
	conn      string
	database  string
	category  dbCategory
	name      *widget.Entry
	columns   []domain.ColumnSpec
	indexes   []domain.IndexSpec
	fieldSel  int
	indexSel  int
	loading   bool
	fieldGrid *widget.Table
	indexGrid *widget.Table

	fName, fType, fDefault, fComment *widget.Entry
	fNullable, fPK, fAuto            *widget.Check
	iName, iCols                     *widget.Entry
	iUnique                          *widget.Check
	engine, charset, collation       *widget.Entry
	tableComment                     *widget.Entry
	ifNotExists                      *widget.Check
}

// newTableDesigner 创建嵌入多文档页签的表结构编辑器（不再使用弹窗）。
func newTableDesigner(u *ui, conn, database string, cat dbCategory) *tableDesigner {
	defaultName := "new_table"
	if cat == categoryDocument {
		defaultName = "new_collection"
	}
	d := &tableDesigner{
		u: u, conn: conn, database: database, category: cat,
		fieldSel: -1, indexSel: -1,
	}
	d.name = widget.NewEntry()
	d.name.SetText(defaultName)
	d.name.SetPlaceHolder("对象名称")
	// 预置一个主键字段，打开即可编辑，避免空白网格。
	d.columns = []domain.ColumnSpec{{
		Name: "id", Type: "BIGINT", Nullable: false,
		PrimaryKey: true, AutoIncrement: true,
	}}
	return d
}

// Build 返回可放入 tab 的内容：顶部对象信息 → 字段/索引/表选项页签 → 生成脚本按钮。
func (d *tableDesigner) Build() fyne.CanvasObject {
	head := container.NewHBox(
		widget.NewLabel("新建"+d.category.objectName()+" · 表结构"),
		widget.NewForm(
			widget.NewFormItem("名称", d.name),
		),
		layout.NewSpacer(),
		widget.NewLabel("库 "+d.database+" · 连接 "+d.conn),
	)
	tabs := container.NewAppTabs(
		container.NewTabItem("字段", d.buildFieldsTab()),
		container.NewTabItem("索引", d.buildIndexesTab()),
		container.NewTabItem("表选项", d.buildOptionsTab()),
	)
	generate := widget.NewButton("生成脚本", func() { d.generate() })
	generate.Importance = widget.HighImportance
	footer := container.NewHBox(
		widget.NewLabel("所有变更先生成方言脚本，确认后才执行"),
		layout.NewSpacer(), generate,
	)
	return container.NewBorder(
		container.NewVBox(head, widget.NewSeparator()), footer, nil, nil, tabs,
	)
}

func (d *tableDesigner) buildFieldsTab() fyne.CanvasObject {
	d.fieldGrid = widget.NewTable(
		func() (int, int) { return len(d.columns) + 1, len(designerColumnHeaders) },
		func() fyne.CanvasObject { return newCellLabel() },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(cellText(d.columnCell(id)))
		},
	)
	sizeSchemaColumns(d.fieldGrid, designerColumnHeaders, nil)
	d.fieldGrid.OnSelected = func(id widget.TableCellID) {
		if id.Row <= 0 || id.Row-1 >= len(d.columns) {
			return
		}
		d.fieldSel = id.Row - 1
		d.loadColumnForm()
	}

	d.fName = widget.NewEntry()
	d.fType = widget.NewEntry()
	d.fDefault = widget.NewEntry()
	d.fComment = widget.NewEntry()
	d.fDefault.SetPlaceHolder("留空表示无默认值")
	d.fNullable = widget.NewCheck("允许 NULL", nil)
	d.fPK = widget.NewCheck("主键", nil)
	d.fAuto = widget.NewCheck("自增", nil)
	bind := func() {
		d.fName.OnChanged = func(string) { d.syncColumn() }
		d.fType.OnChanged = func(string) { d.syncColumn() }
		d.fDefault.OnChanged = func(string) { d.syncColumn() }
		d.fComment.OnChanged = func(string) { d.syncColumn() }
		d.fNullable.OnChanged = func(bool) { d.syncColumn() }
		d.fPK.OnChanged = func(bool) { d.syncColumn() }
		d.fAuto.OnChanged = func(bool) { d.syncColumn() }
	}
	bind()

	form := widget.NewForm(
		widget.NewFormItem("字段名", d.fName),
		widget.NewFormItem("类型", d.fType),
		widget.NewFormItem("默认值", d.fDefault),
		widget.NewFormItem("注释", d.fComment),
		widget.NewFormItem("约束", container.NewHBox(d.fNullable, d.fPK, d.fAuto)),
	)
	toolbar := container.NewHBox(
		widget.NewButton("+ 添加字段", d.addColumn),
		widget.NewButton("− 删除字段", d.removeColumn),
		widget.NewButton("↑", func() { d.moveColumn(-1) }),
		widget.NewButton("↓", func() { d.moveColumn(1) }),
		layout.NewSpacer(),
		widget.NewLabel("单击网格中的字段行，在下方编辑属性"),
	)
	return container.NewBorder(
		toolbar, container.NewVBox(widget.NewSeparator(), form), nil, nil, d.fieldGrid,
	)
}

func (d *tableDesigner) columnCell(id widget.TableCellID) string {
	if id.Row == 0 {
		if id.Col < len(designerColumnHeaders) {
			return designerColumnHeaders[id.Col]
		}
		return ""
	}
	if id.Row-1 >= len(d.columns) {
		return ""
	}
	return schemaColumnCell(columnSpecToInfo(d.columns[id.Row-1]), id.Col)
}

func (d *tableDesigner) loadColumnForm() {
	if d.fieldSel < 0 || d.fieldSel >= len(d.columns) {
		return
	}
	d.loading = true
	defer func() { d.loading = false }()
	c := d.columns[d.fieldSel]
	d.fName.SetText(c.Name)
	d.fType.SetText(c.Type)
	d.fComment.SetText(c.Comment)
	if c.Default != nil {
		d.fDefault.SetText(*c.Default)
	} else {
		d.fDefault.SetText("")
	}
	d.fNullable.SetChecked(c.Nullable)
	d.fPK.SetChecked(c.PrimaryKey)
	d.fAuto.SetChecked(c.AutoIncrement)
}

func (d *tableDesigner) syncColumn() {
	if d.loading || d.fieldSel < 0 || d.fieldSel >= len(d.columns) {
		return
	}
	c := &d.columns[d.fieldSel]
	c.Name = strings.TrimSpace(d.fName.Text)
	c.Type = strings.TrimSpace(d.fType.Text)
	c.Comment = d.fComment.Text
	c.Nullable = d.fNullable.Checked
	c.PrimaryKey = d.fPK.Checked
	c.AutoIncrement = d.fAuto.Checked
	c.Default = nil
	if value := d.fDefault.Text; value != "" {
		c.Default = &value
	}
	d.fieldGrid.Refresh()
}

func (d *tableDesigner) addColumn() {
	d.syncColumn()
	d.columns = append(d.columns, domain.ColumnSpec{
		Name: fmt.Sprintf("column_%d", len(d.columns)+1), Type: "VARCHAR(255)", Nullable: true,
	})
	d.fieldSel = len(d.columns) - 1
	d.fieldGrid.Refresh()
	d.loadColumnForm()
}

func (d *tableDesigner) removeColumn() {
	if d.fieldSel < 0 || d.fieldSel >= len(d.columns) {
		return
	}
	d.columns = append(d.columns[:d.fieldSel], d.columns[d.fieldSel+1:]...)
	if d.fieldSel >= len(d.columns) {
		d.fieldSel = len(d.columns) - 1
	}
	d.fieldGrid.Refresh()
	d.loadColumnForm()
}

func (d *tableDesigner) moveColumn(delta int) {
	next := d.fieldSel + delta
	if d.fieldSel < 0 || next < 0 || next >= len(d.columns) {
		return
	}
	d.columns[d.fieldSel], d.columns[next] = d.columns[next], d.columns[d.fieldSel]
	d.fieldSel = next
	d.fieldGrid.Refresh()
	d.fieldGrid.Select(widget.TableCellID{Row: next + 1})
}

func (d *tableDesigner) buildIndexesTab() fyne.CanvasObject {
	d.indexGrid = widget.NewTable(
		func() (int, int) { return len(d.indexes) + 1, len(designerIndexHeaders) },
		func() fyne.CanvasObject { return newCellLabel() },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(cellText(d.indexCell(id)))
		},
	)
	sizeTableColumns(d.indexGrid, designerIndexHeaders, nil)
	d.indexGrid.OnSelected = func(id widget.TableCellID) {
		if id.Row <= 0 || id.Row-1 >= len(d.indexes) {
			return
		}
		d.indexSel = id.Row - 1
		d.loadIndexForm()
	}
	d.iName = widget.NewEntry()
	d.iCols = widget.NewEntry()
	d.iCols.SetPlaceHolder("多个字段用英文逗号分隔")
	d.iUnique = widget.NewCheck("唯一索引", nil)
	d.iName.OnChanged = func(string) { d.syncIndex() }
	d.iCols.OnChanged = func(string) { d.syncIndex() }
	d.iUnique.OnChanged = func(bool) { d.syncIndex() }

	form := widget.NewForm(
		widget.NewFormItem("索引名", d.iName),
		widget.NewFormItem("字段列表", d.iCols),
		widget.NewFormItem("约束", container.NewHBox(d.iUnique)),
	)
	toolbar := container.NewHBox(
		widget.NewButton("+ 添加索引", d.addIndex),
		widget.NewButton("− 删除索引", d.removeIndex),
		layout.NewSpacer(),
		widget.NewLabel("单击网格中的索引行，在下方编辑属性"),
	)
	return container.NewBorder(
		toolbar, container.NewVBox(widget.NewSeparator(), form), nil, nil, d.indexGrid,
	)
}

func (d *tableDesigner) indexCell(id widget.TableCellID) string {
	if id.Row == 0 {
		if id.Col < len(designerIndexHeaders) {
			return designerIndexHeaders[id.Col]
		}
		return ""
	}
	if id.Row-1 >= len(d.indexes) {
		return ""
	}
	idx := d.indexes[id.Row-1]
	switch id.Col {
	case 0:
		return idx.Name
	case 1:
		return strings.Join(idx.Columns, ", ")
	case 2:
		return boolText(idx.Unique)
	}
	return ""
}

func (d *tableDesigner) loadIndexForm() {
	if d.indexSel < 0 || d.indexSel >= len(d.indexes) {
		return
	}
	d.loading = true
	defer func() { d.loading = false }()
	idx := d.indexes[d.indexSel]
	d.iName.SetText(idx.Name)
	d.iCols.SetText(strings.Join(idx.Columns, ", "))
	d.iUnique.SetChecked(idx.Unique)
}

func (d *tableDesigner) syncIndex() {
	if d.loading || d.indexSel < 0 || d.indexSel >= len(d.indexes) {
		return
	}
	idx := &d.indexes[d.indexSel]
	idx.Name = strings.TrimSpace(d.iName.Text)
	idx.Unique = d.iUnique.Checked
	parts := strings.Split(d.iCols.Text, ",")
	idx.Columns = make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			idx.Columns = append(idx.Columns, s)
		}
	}
	d.indexGrid.Refresh()
}

func (d *tableDesigner) addIndex() {
	d.syncIndex()
	d.indexes = append(d.indexes, domain.IndexSpec{
		Name: fmt.Sprintf("idx_%d", len(d.indexes)+1),
	})
	d.indexSel = len(d.indexes) - 1
	d.indexGrid.Refresh()
	d.loadIndexForm()
}

func (d *tableDesigner) removeIndex() {
	if d.indexSel < 0 || d.indexSel >= len(d.indexes) {
		return
	}
	d.indexes = append(d.indexes[:d.indexSel], d.indexes[d.indexSel+1:]...)
	if d.indexSel >= len(d.indexes) {
		d.indexSel = len(d.indexes) - 1
	}
	d.indexGrid.Refresh()
	d.loadIndexForm()
}

func (d *tableDesigner) buildOptionsTab() fyne.CanvasObject {
	d.engine = widget.NewEntry()
	d.engine.SetPlaceHolder("如 InnoDB（不支持该选项的方言会忽略）")
	d.charset = widget.NewEntry()
	d.charset.SetPlaceHolder("如 utf8mb4")
	d.collation = widget.NewEntry()
	d.collation.SetPlaceHolder("如 utf8mb4_general_ci")
	d.tableComment = widget.NewEntry()
	d.ifNotExists = widget.NewCheck("IF NOT EXISTS", nil)
	d.ifNotExists.SetChecked(true)
	return container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("存储引擎", d.engine),
			widget.NewFormItem("字符集", d.charset),
			widget.NewFormItem("排序规则", d.collation),
			widget.NewFormItem("表注释", d.tableComment),
			widget.NewFormItem("存在性检查", d.ifNotExists),
		),
		widget.NewLabel("字段与索引定义会由对应的数据库模块翻译成方言脚本，生成前可先预览。"),
	)
}

func (d *tableDesigner) generate() {
	d.syncColumn()
	d.syncIndex()
	name := strings.TrimSpace(d.name.Text)
	if name == "" {
		d.u.showInfo("输入有误", "请填写表名")
		return
	}
	if len(d.columns) == 0 {
		d.u.showInfo("输入有误", "至少需要定义一个字段")
		return
	}
	for i, c := range d.columns {
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Type) == "" {
			d.u.showInfo("输入有误", fmt.Sprintf("第 %d 个字段的名称与类型不能为空", i+1))
			return
		}
	}
	def := domain.TableDefinition{
		Ref:         domain.ObjectRef{Database: d.database, Name: name},
		Columns:     d.columns,
		Indexes:     d.indexes,
		Engine:      d.engine.Text,
		Charset:     d.charset.Text,
		Collation:   d.collation.Text,
		Comment:     d.tableComment.Text,
		IfNotExists: d.ifNotExists.Checked,
	}
	scriptTitle := "CREATE TABLE 预览"
	if d.category == categoryDocument {
		scriptTitle = "createCollection 预览"
	}
	conn := d.conn
	d.u.async(func() {
		script, err := d.u.core.PreviewCreateTable(context.Background(), conn, def)
		if err != nil {
			d.u.showError(err)
			return
		}
		fyne.Do(func() { d.u.showScriptDialog(scriptTitle, conn, script) })
	})
}
