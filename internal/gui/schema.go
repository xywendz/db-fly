//go:build windows && cgo

package gui

import (
	"context"
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
	schemaIndexHeaders      = []string{"索引名", "列", "唯一", "类型"}
	schemaForeignKeyHeaders = []string{"约束名", "列", "引用表", "引用列"}
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
	foreign  []domain.ForeignKeyInfo
	current  domain.TableSchema
	category dbCategory
	caps     domain.CapabilitySet

	view fyne.CanvasObject
	info *widget.Label
	tabs *container.AppTabs

	colItem     *container.TabItem
	idxItem     *container.TabItem
	fkItem      *container.TabItem
	optItem     *container.TabItem
	commentItem *container.TabItem
	ddlItem     *container.TabItem

	colTable *widget.Table
	idxTable *widget.Table
	fkTable  *widget.Table
	ddl      *widget.Entry

	// 字段页：上方网格 + 可拖动的底部字段属性面板
	fieldSplit   *container.Split
	detailHeader *widget.Label
	detailType   *widget.Entry
	detailDef    *widget.Entry
	detailNull   *widget.Check
	detailAuto   *widget.Check
	detailUnsign *widget.Check
	detailZero   *widget.Check
	detailVirt   *widget.Check
	detailKey    *widget.Check
	detailCmt    *widget.Entry

	// 表选项 / 注释页
	optForm   *widget.Form
	optEngine *widget.Entry
	optChar   *widget.Entry
	optColl   *widget.Entry
	optCmt    *widget.Entry
	comment   *widget.Entry

	// 上下文操作条：随页签切换（字段 / 索引）
	colToolbar *fyne.Container
	idxToolbar *fyne.Container
	colHint    *widget.Label
	idxHint    *widget.Label
	colSel     widget.TableCellID
	idxSel     widget.TableCellID
	hasColSel  bool
	hasIdxSel  bool

	addColBtn    *widget.Button
	insertColBtn *widget.Button
	modifyColBtn *widget.Button
	renameColBtn *widget.Button
	dropColBtn   *widget.Button
	addIdxBtn    *widget.Button
	dropIdxBtn   *widget.Button

	refreshBtn   *widget.Button
	ddlBtn       *widget.Button
	alterBtn     *widget.Button
	validatorBtn *widget.Button
	createBtn    *widget.Button
	dropBtn      *widget.Button
	optionsBtn   *widget.Button
	renameBtn    *widget.Button
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
	v.fkTable = widget.NewTable(
		func() (int, int) {
			v.mu.RLock()
			defer v.mu.RUnlock()
			return len(v.foreign) + 1, len(schemaForeignKeyHeaders)
		},
		func() fyne.CanvasObject { return newCellLabel() },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(cellText(v.foreignKeyCell(id)))
		},
	)
	v.colTable.OnSelected = func(id widget.TableCellID) {
		v.colSel, v.hasColSel = id, id.Row > 0
		v.updateSelectionHints()
	}
	v.idxTable.OnSelected = func(id widget.TableCellID) {
		v.idxSel, v.hasIdxSel = id, id.Row > 0
		v.updateSelectionHints()
	}
	sizeSchemaColumns(v.colTable, schemaColumnHeaders, nil)
	sizeTableColumns(v.idxTable, schemaIndexHeaders, nil)
	sizeTableColumns(v.fkTable, schemaForeignKeyHeaders, nil)

	v.ddl = widget.NewMultiLineEntry()
	v.ddl.Wrapping = fyne.TextWrapOff
	v.comment = widget.NewMultiLineEntry()
	v.comment.Wrapping = fyne.TextWrapWord

	// 参考 Navicat：字段页上半部为字段网格，下半部为可拖动的「字段属性」面板。
	v.colHint = widget.NewLabel("未选中字段")
	v.idxHint = widget.NewLabel("未选中索引")
	v.fieldSplit = container.NewVSplit(
		container.NewBorder(nil, v.colHint, nil, nil, v.colTable),
		v.buildFieldDetail(),
	)
	v.fieldSplit.Offset = 0.66

	// 上下文操作条置于页签上方，随当前页签切换字段 / 索引动作。
	v.addColBtn = widget.NewButton("+ 添加字段", v.showAddColumn)
	v.insertColBtn = widget.NewButton("插入字段", v.showInsertColumn)
	v.modifyColBtn = widget.NewButton("✎ 修改字段类型", v.showModifyColumn)
	v.renameColBtn = widget.NewButton("↻ 修改字段名称", v.showRenameColumn)
	v.dropColBtn = widget.NewButton("− 删除字段", v.showDropColumn)
	v.addIdxBtn = widget.NewButton("+ 新增索引", v.showAddIndex)
	v.dropIdxBtn = widget.NewButton("− 删除索引", v.showDropIndex)
	v.colToolbar = container.NewHBox(
		v.addColBtn, v.insertColBtn, widget.NewSeparator(),
		v.modifyColBtn, v.renameColBtn, v.dropColBtn,
	)
	v.idxToolbar = container.NewHBox(v.addIdxBtn, v.dropIdxBtn)

	v.colItem = container.NewTabItem("字段", v.fieldSplit)
	v.idxItem = container.NewTabItem("索引",
		container.NewBorder(nil, v.idxHint, nil, nil, v.idxTable))
	v.fkItem = container.NewTabItem("外键", v.fkTable)
	v.optItem = container.NewTabItem("表选项", v.buildOptionsTab())
	v.commentItem = container.NewTabItem("注释", v.buildCommentTab())
	v.ddlItem = container.NewTabItem("SQL 预览", v.ddl)
	v.tabs = container.NewAppTabs(
		v.colItem, v.idxItem, v.fkItem, v.optItem, v.commentItem, v.ddlItem,
	)
	v.tabs.OnSelected = func(*container.TabItem) { v.syncContextToolbar() }

	v.refreshBtn = widget.NewButton("刷新", v.reload)
	v.ddlBtn = widget.NewButton("查看建表 SQL", v.showDDL)
	v.alterBtn = widget.NewButton("高级批量修改", v.showAlterForm)
	v.validatorBtn = widget.NewButton("修改集合校验器", v.showCollectionValidator)
	v.createBtn = widget.NewButton("新建表", v.showCreateTable)
	v.dropBtn = widget.NewButton("删除表", v.showDropTable)
	v.optionsBtn = widget.NewButton("修改表选项", v.showTableOptions)
	v.renameBtn = widget.NewButton("重命名表", v.showRenameTable)
	objectBar := container.NewHBox(
		v.refreshBtn, widget.NewSeparator(),
		v.ddlBtn, v.renameBtn, v.alterBtn,
		widget.NewSeparator(), v.createBtn, v.dropBtn, v.validatorBtn,
	)
	contextBar := container.NewHBox(v.colToolbar, v.idxToolbar)
	toolbar := container.NewVBox(objectBar, contextBar, widget.NewSeparator())
	v.view = container.NewBorder(
		container.NewVBox(toolbar, v.info), nil, nil, nil, v.tabs,
	)
	return v.view
}

// buildFieldDetail 参考图的底部字段属性面板。所有控件只读：
// 本项目所有 DDL 必须走「结构化变更 → 脚本预览 → 确认」，不在网格里直接改后保存。
func (v *schemaView) buildFieldDetail() fyne.CanvasObject {
	v.detailHeader = widget.NewLabel("未选中字段")
	v.detailType = widget.NewEntry()
	v.detailDef = widget.NewEntry()
	v.detailCmt = widget.NewEntry()
	v.detailNull = widget.NewCheck("允许 NULL", nil)
	v.detailAuto = widget.NewCheck("自增", nil)
	v.detailKey = widget.NewCheck("主键", nil)
	v.detailUnsign = widget.NewCheck("无符号 Unsigned", nil)
	v.detailZero = widget.NewCheck("补零 Zerofill", nil)
	v.detailVirt = widget.NewCheck("虚拟列", nil)
	for _, entry := range []*widget.Entry{v.detailType, v.detailDef, v.detailCmt} {
		entry.Disable()
	}
	for _, check := range []*widget.Check{
		v.detailNull, v.detailAuto, v.detailKey, v.detailUnsign, v.detailZero, v.detailVirt,
	} {
		check.Disable()
	}
	form := widget.NewForm(
		widget.NewFormItem("完整类型", v.detailType),
		widget.NewFormItem("默认值", v.detailDef),
		widget.NewFormItem("注释", v.detailCmt),
		widget.NewFormItem("约束", container.NewHBox(v.detailNull, v.detailAuto, v.detailKey)),
		widget.NewFormItem("数值属性", container.NewHBox(v.detailUnsign, v.detailZero, v.detailVirt)),
	)
	scroll := container.NewVScroll(form)
	scroll.SetMinSize(fyne.NewSize(0, 160))
	return container.NewBorder(
		container.NewVBox(widget.NewSeparator(), v.detailHeader),
		widget.NewLabel("只读属性 · 修改字段请使用上方按钮生成脚本预览后执行"),
		nil, nil, scroll,
	)
}

func (v *schemaView) buildOptionsTab() fyne.CanvasObject {
	v.optEngine = widget.NewEntry()
	v.optChar = widget.NewEntry()
	v.optColl = widget.NewEntry()
	v.optCmt = widget.NewEntry()
	for _, e := range []*widget.Entry{v.optEngine, v.optChar, v.optColl, v.optCmt} {
		e.Disable()
	}
	v.optForm = widget.NewForm(
		widget.NewFormItem("存储引擎", v.optEngine),
		widget.NewFormItem("字符集", v.optChar),
		widget.NewFormItem("排序规则", v.optColl),
		widget.NewFormItem("表注释", v.optCmt),
	)
	return container.NewBorder(
		widget.NewLabel("表选项（元数据只读）"),
		container.NewHBox(widget.NewButton("修改表选项 / 表注释", v.showTableOptions)),
		nil, nil, v.optForm,
	)
}

func (v *schemaView) buildCommentTab() fyne.CanvasObject {
	return container.NewBorder(
		widget.NewLabel("表注释与字段注释（元数据只读）"), nil, nil, nil, v.comment,
	)
}

// syncContextToolbar 让字段 / 索引两套动作按钮随页签切换，工具条不占网格空间。
func (v *schemaView) syncContextToolbar() {
	rel := v.category == categoryRelational && v.caps.Has(domain.CapAlterTable)
	selected := v.tabs.Selected()
	showObj(v.colToolbar, rel && (selected == nil || selected == v.colItem))
	showObj(v.idxToolbar, rel && selected == v.idxItem)
}

func (v *schemaView) updateSelectionHints() {
	if col, ok := v.selectedColumn(); ok {
		v.colHint.SetText(fmt.Sprintf("选中字段: %s (%s)", col.Name, columnRawType(col)))
		v.detailHeader.SetText("字段: " + col.Name)
		parts := parseColumnType(columnRawType(col), col.Length)
		v.detailType.SetText(columnRawType(col))
		if col.HasDefault {
			v.detailDef.SetText(col.Default)
		} else {
			v.detailDef.SetText("-")
		}
		v.detailCmt.SetText(col.Comment)
		v.detailNull.SetChecked(col.Nullable)
		v.detailAuto.SetChecked(col.AutoIncrement)
		v.detailKey.SetChecked(col.PrimaryKey)
		v.detailUnsign.SetChecked(parts.Unsigned)
		v.detailZero.SetChecked(parts.Zerofill)
		v.detailVirt.SetChecked(false)
	} else {
		v.colHint.SetText("未选中字段（单击字段网格中的一行）")
		v.detailHeader.SetText("未选中字段")
		v.detailType.SetText("")
		v.detailDef.SetText("")
		v.detailCmt.SetText("")
		v.detailNull.SetChecked(false)
		v.detailAuto.SetChecked(false)
		v.detailKey.SetChecked(false)
		v.detailUnsign.SetChecked(false)
		v.detailZero.SetChecked(false)
		v.detailVirt.SetChecked(false)
	}
	if idx, ok := v.selectedIndex(); ok {
		v.idxHint.SetText("选中索引: " + idx.Name)
	} else {
		v.idxHint.SetText("未选中索引（单击索引网格中的一行）")
	}
}

// setCategory 按数据库类别裁剪结构页的页签与按钮：
// 关系型展示字段 / 索引 / 外键 / 建表 DDL；文档型展示字段采样 / 索引 / 集合定义。
func (v *schemaView) setCategory(cat dbCategory, caps domain.CapabilitySet) {
	v.category = cat
	v.caps = caps
	show := func(obj fyne.CanvasObject, visible bool) {
		if visible {
			obj.Show()
		} else {
			obj.Hide()
		}
	}
	switch cat {
	case categoryDocument:
		v.colItem.Text = "字段 (采样)"
		v.ddlItem.Text = "集合定义"
		v.tabs.SetItems([]*container.TabItem{v.colItem, v.idxItem, v.commentItem, v.ddlItem})
		v.ddlBtn.SetText("查看集合定义")
		v.createBtn.SetText("新建集合")
		v.dropBtn.SetText("删除集合")
	case categoryKeyValue:
		v.colItem.Text = "键信息"
		v.ddlItem.Text = "等价命令"
		v.tabs.SetItems([]*container.TabItem{v.colItem, v.commentItem, v.ddlItem})
		v.ddlBtn.SetText("查看等价命令")
		v.createBtn.SetText("新建键")
		v.dropBtn.SetText("删除键")
	default:
		v.colItem.Text = "字段"
		v.ddlItem.Text = "SQL 预览"
		v.tabs.SetItems([]*container.TabItem{
			v.colItem, v.idxItem, v.fkItem, v.optItem, v.commentItem, v.ddlItem,
		})
		v.ddlBtn.SetText("查看建表 SQL")
		v.createBtn.SetText("新建表")
		v.dropBtn.SetText("删除表")
	}
	v.tabs.Refresh()
	relational := cat == categoryRelational
	show(v.fieldSplit, true)
	show(v.colHint, true)
	show(v.idxHint, true)
	v.syncContextToolbar()
	show(v.refreshBtn, true)
	show(v.ddlBtn, caps.Has(domain.CapShowCreate))
	show(v.alterBtn, relational && caps.Has(domain.CapAlterTable))
	show(v.optionsBtn, relational && caps.Has(domain.CapAlterTable))
	show(v.renameBtn, relational && caps.Has(domain.CapAlterTable))
	show(v.validatorBtn, cat == categoryDocument && caps.Has(domain.CapAlterTable))
	show(v.createBtn, caps.Has(domain.CapCreateTable) && cat != categoryKeyValue)
	show(v.dropBtn, caps.Has(domain.CapDropObject) && cat != categoryKeyValue)
	v.updateSelectionHints()
}

func (v *schemaView) setTarget(conn, database, table string) {
	v.mu.Lock()
	v.conn, v.database, v.table = conn, database, table
	v.mu.Unlock()
	v.caps = v.u.capsFor(conn)
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
	return schemaColumnCell(v.columns[id.Row-1], id.Col)
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

func (v *schemaView) foreignKeyCell(id widget.TableCellID) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if id.Row == 0 {
		if id.Col < len(schemaForeignKeyHeaders) {
			return schemaForeignKeyHeaders[id.Col]
		}
		return ""
	}
	if id.Row-1 >= len(v.foreign) {
		return ""
	}
	fk := v.foreign[id.Row-1]
	switch id.Col {
	case 0:
		return fk.Name
	case 1:
		return strings.Join(fk.Columns, ", ")
	case 2:
		target := fk.RefTable
		if fk.RefDatabase != "" {
			target = fk.RefDatabase + "." + target
		}
		return target
	case 3:
		return strings.Join(fk.RefColumns, ", ")
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
	v.foreign = schema.ForeignKeys
	category := v.category
	v.mu.Unlock()

	switch category {
	case categoryDocument:
		v.info.SetText(fmt.Sprintf("%s · 文档集合 · %d 个采样字段 / %d 索引 · %s",
			schema.Ref.String(), len(schema.Columns), len(schema.Indexes), schema.Comment))
	case categoryKeyValue:
		v.info.SetText(fmt.Sprintf("%s · %s", schema.Ref.String(), schema.Comment))
	default:
		v.info.SetText(fmt.Sprintf("%s · 引擎 %s · 字符集 %s · %d 字段 / %d 索引 / %d 外键",
			schema.Ref.String(), schema.Engine, schema.Charset,
			len(schema.Columns), len(schema.Indexes), len(schema.ForeignKeys)))
	}
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
	v.optEngine.SetText(schema.Engine)
	v.optChar.SetText(schema.Charset)
	v.optColl.SetText(schema.Collation)
	v.optCmt.SetText(schema.Comment)
	v.comment.SetText(buildSchemaComment(schema))
	v.sizeColumnsToData()
	v.colTable.Refresh()
	v.idxTable.Refresh()
	v.fkTable.Refresh()
	v.hasColSel, v.hasIdxSel = false, false
	v.updateSelectionHints()
	v.u.setStatus("结构加载完成：%s", schema.Ref.String())
}

// sizeColumnsToData 按最新的字段/索引内容重新计算列宽，避免长文本互相覆盖。
func (v *schemaView) sizeColumnsToData() {
	v.mu.RLock()
	colSample := make([][]string, 0, len(v.columns))
	for _, c := range v.columns {
		parts := parseColumnType(columnRawType(c), c.Length)
		colSample = append(colSample, []string{
			c.Name, parts.Base, parts.Length, parts.Decimals, "", "", "PK 1", c.Comment,
		})
	}
	idxSample := make([][]string, 0, len(v.indexes))
	for _, idx := range v.indexes {
		idxSample = append(idxSample, []string{idx.Name, strings.Join(idx.Columns, ", "), "", idx.IndexType})
	}
	fkSample := make([][]string, 0, len(v.foreign))
	for _, fk := range v.foreign {
		fkSample = append(fkSample, []string{
			fk.Name, strings.Join(fk.Columns, ", "), fk.RefTable, strings.Join(fk.RefColumns, ", "),
		})
	}
	v.mu.RUnlock()

	sizeSchemaColumns(v.colTable, schemaColumnHeaders, colSample)
	sizeTableColumns(v.idxTable, schemaIndexHeaders, idxSample)
	sizeTableColumns(v.fkTable, schemaForeignKeyHeaders, fkSample)
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
			title := "建表语句"
			switch v.category {
			case categoryDocument:
				title = "集合定义"
			case categoryKeyValue:
				title = "等价创建命令"
			}
			dialog.ShowCustom(title+" · "+ref.String(), "关闭", scroll, v.u.win)
		})
	})
}

// showAlterForm 关系型打开结构化改表表单，文档型打开集合校验器表单；
// 先确保当前结构信息是最新的，避免右键动作使用上一次加载的字段列表。
func (v *schemaView) showAlterForm() {
	if v.category == categoryDocument {
		v.showCollectionValidator()
		return
	}
	v.ensureCurrent(v.openAlterForm)
}

// showCollectionValidator 用 collMod 的整体 validator 定义修改文档集合结构。
func (v *schemaView) showCollectionValidator() {
	v.mu.RLock()
	conn, database, table := v.conn, v.database, v.table
	v.mu.RUnlock()
	if table == "" {
		v.u.showInfo("提示", "请先选择一个集合")
		return
	}
	validator := widget.NewMultiLineEntry()
	validator.Wrapping = fyne.TextWrapOff
	validator.SetText(`{
  "$jsonSchema": {
    "bsonType": "object",
    "required": ["name"],
    "properties": {
      "name": {"bsonType": "string", "description": "名称"}
    }
  }
}`)
	content := container.NewBorder(
		widget.NewLabel("validator 会整体替换集合校验器，请提供完整定义（JSON 对象）："),
		nil, nil, nil, container.NewScroll(validator),
	)
	var d *dialog.CustomDialog
	generate := widget.NewButton("生成脚本", func() {
		if strings.TrimSpace(validator.Text) == "" {
			v.u.showInfo("提示", "请填写 validator JSON")
			return
		}
		d.Hide()
		ref := domain.ObjectRef{Database: database, Name: table}
		changes := []domain.SchemaChange{{
			Kind: domain.ChangeCollectionOpts, Validator: validator.Text,
		}}
		v.u.async(func() {
			script, err := v.u.core.PreviewAlterTable(context.Background(), conn, ref, changes)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialog("修改集合校验器 预览", conn, script) })
		})
	})
	d = dialog.NewCustom("修改集合校验器 · "+table, "关闭",
		container.NewBorder(nil, container.NewHBox(widget.NewButton("取消", func() { d.Hide() }), generate),
			nil, nil, content), v.u.win)
	d.Resize(fyne.NewSize(700, 480))
	d.Show()
}

func (v *schemaView) openAlterForm() {
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

// showCreateTable 在独立页签中打开表结构样式的设计器，不再弹窗、不再编辑 JSON。
func (v *schemaView) showCreateTable() {
	v.mu.RLock()
	conn, database := v.conn, v.database
	v.mu.RUnlock()
	if conn == "" || database == "" {
		v.u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	v.u.tabs.openCreateTable(conn, database, categoryOf(v.u.capsFor(conn)))
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
	objectName, scriptTitle := "表", "DROP TABLE 预览"
	switch v.category {
	case categoryDocument:
		objectName, scriptTitle = "集合", "dropCollection 预览"
	case categoryKeyValue:
		objectName, scriptTitle = "键", "DEL 预览"
	}
	dialog.ShowConfirm("删除"+objectName,
		"即将生成删除"+objectName+" "+ref.String()+" 的脚本，数据不可恢复。是否继续？", func(ok bool) {
			if !ok {
				return
			}
			v.u.async(func() {
				script, err := v.u.core.PreviewDropObject(context.Background(), conn, ref)
				if err != nil {
					v.u.showError(err)
					return
				}
				fyne.Do(func() { v.u.showScriptDialogWith(scriptTitle, conn, script, false) })
			})
		}, v.u.win)
}
