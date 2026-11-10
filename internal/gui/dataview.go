//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/domain"
)

var (
	pageSizeOptions = []string{"25", "50", "100", "200"}
	filterOperators = []string{"=", "<>", ">", ">=", "<", "<=", "LIKE", "NOT LIKE", "IN", "NOT IN", "IS NULL", "IS NOT NULL"}
	conditionJoins  = []string{"AND", "OR"}
	sortDirections  = []string{"升序", "降序"}
)

type conditionRow struct {
	box    *fyne.Container
	lead   *widget.Label
	join   *widget.Select
	field  *widget.Select
	op     *widget.Select
	value  *widget.Entry
	remove *widget.Button
}

type orderRow struct {
	box    *fyne.Container
	field  *widget.Select
	dir    *widget.Select
	remove *widget.Button
}

type cellKey struct{ row, col int }

// dataView 是表数据页。关系型使用结构化「过滤 / 排序」构建器、分页与单元格编辑器；
// 文档型 / 键值型保留各自的 filter JSON / 键匹配输入。
type dataView struct {
	u    *ui
	view fyne.CanvasObject
	grid *resultGrid
	info *widget.Label

	// 非关系型的简单条件输入
	simpleBar   *fyne.Container
	filter      *widget.Entry
	sort        *widget.Entry
	filterLabel *widget.Label
	sortLabel   *widget.Label

	// 关系型的过滤 / 排序构建器
	builder      *fyne.Container
	filterBox    *fyne.Container
	sortBox      *fyne.Container
	filterRows   []*conditionRow
	orderRows    []*orderRow
	preview      *widget.Label
	addCondBtn   *widget.Button
	addSortBtn   *widget.Button
	applyBtn     *widget.Button
	resetBtn     *widget.Button
	filterToggle *widget.Button
	builderOpen  bool

	// 工具条与分页
	toolbar       *fyne.Container
	pageSizeBox   *fyne.Container
	pageSizeSel   *widget.Select
	refreshBtn    *widget.Button
	exportBtn     *widget.Button
	importBtn     *widget.Button
	editBtn       *widget.Button
	truncateBtn   *widget.Button
	emptyBtn      *widget.Button
	addRowBtn     *widget.Button
	deleteRowBtn  *widget.Button
	cancelEditBtn *widget.Button
	firstBtn      *widget.Button
	prevBtn       *widget.Button
	nextBtn       *widget.Button
	pageLabel     *widget.Label
	recordBar     *fyne.Container
	pagerBar      *fyne.Container
	infoBar       *fyne.Container
	statement     *widget.Label
	recordLabel   *widget.Label
	page          int
	hasNext       bool
	rowCount      int

	// 记录级暂存：单元格修改 / 新增行 / 删除行
	pendingLabel   *widget.Label
	commitBtn      *widget.Button
	revertBtn      *widget.Button
	pending        map[cellKey]string
	newRows        [][]string
	pendingDeletes map[int]bool
	selectedRow    int

	columns    []domain.ColumnInfo
	keyColumns []string

	conn     string
	database string
	table    string
	caps     domain.CapabilitySet
	category dbCategory
}

func newDataView(u *ui) *dataView {
	return &dataView{
		u: u, pending: make(map[cellKey]string),
		pendingDeletes: make(map[int]bool), builderOpen: true, selectedRow: -1,
	}
}

func showObj(obj fyne.CanvasObject, visible bool) {
	if visible {
		obj.Show()
	} else {
		obj.Hide()
	}
}

func (v *dataView) build() fyne.CanvasObject {
	v.grid = newResultGrid(v.u)
	v.grid.showTypes = true
	v.grid.onActivated = func(id widget.TableCellID) { v.grid.startEditing(id) }
	v.grid.onCommitted = v.stageCell
	v.grid.cellDisplay = v.cellDisplay
	v.grid.editValue = v.cellValue
	v.grid.extraRows = func() int { return len(v.newRows) }
	v.grid.onCellSelected = func(id widget.TableCellID) {
		v.selectedRow = id.Row - 1
		v.updateRecordLabel()
	}
	v.info = widget.NewLabel("请在左侧选择一张表")
	v.info.Truncation = fyne.TextTruncateEllipsis
	v.info.Wrapping = fyne.TextWrapOff

	v.refreshBtn = widget.NewButton("刷新", v.reload)
	v.exportBtn = widget.NewButton("导出 CSV", func() { exportPage(v.u, v.grid.currentPage()) })
	v.importBtn = widget.NewButton("导入文件", func() {
		showImportDialog(v.u, v.conn, v.database, v.table, v.reload)
	})
	v.editBtn = widget.NewButton("编辑选中行", func() {
		showRowEditor(v.u, v.conn, v.database, v.table, v.grid, v.reload)
	})
	v.truncateBtn = widget.NewButton("TRUNCATE", func() {
		v.showTableAction(domain.TableActionTruncate)
	})
	v.emptyBtn = widget.NewButton("清空数据(DELETE)", func() {
		v.showTableAction(domain.TableActionEmpty)
	})
	v.pageSizeSel = widget.NewSelect(pageSizeOptions, func(string) {
		v.page = 0
		if v.conn != "" && v.table != "" {
			v.reload()
		}
	})
	v.pageSizeSel.SetSelected(strconv.Itoa(defaultPreviewLimit))
	v.pageSizeBox = container.NewHBox(widget.NewLabel("每页"), v.pageSizeSel, widget.NewLabel("行"))

	// 折叠开关常驻主工具条，收起条件卡片后仍可再次展开。
	v.filterToggle = widget.NewButton("过滤 & 排序 ▲", v.toggleBuilder)
	v.toolbar = container.NewHBox(
		v.refreshBtn, v.filterToggle, widget.NewSeparator(),
		v.importBtn, v.exportBtn, v.editBtn,
		widget.NewSeparator(), v.truncateBtn, v.emptyBtn,
		layout.NewSpacer(), v.pageSizeBox,
	)

	// 非关系型：单行的 filter / sort 输入
	v.filterLabel = widget.NewLabel("过滤")
	v.sortLabel = widget.NewLabel("排序")
	v.filter = widget.NewEntry()
	v.sort = widget.NewEntry()
	simpleActions := container.NewHBox(
		widget.NewButton("应用条件", v.reload),
		widget.NewButton("清除条件", func() {
			v.filter.SetText("")
			v.sort.SetText("")
			v.reload()
		}),
	)
	v.simpleBar = container.NewVBox(
		container.NewHBox(v.filterLabel, v.filter, v.sortLabel, v.sort, simpleActions),
	)

	// 关系型：可折叠的「过滤 / 排序」条件卡片（Builder / 文本双模式见预览标签）
	v.filterBox = container.NewVBox()
	v.sortBox = container.NewVBox()
	v.preview = widget.NewLabel("")
	v.preview.Wrapping = fyne.TextWrapWord
	v.addCondBtn = widget.NewButton("+ 添加条件", v.addFilterRow)
	v.addSortBtn = widget.NewButton("+ 添加排序", v.addOrderRow)
	v.applyBtn = widget.NewButton("应用过滤 & 排序", func() { v.page = 0; v.reload() })
	v.resetBtn = widget.NewButton("重置条件", v.resetConditions)

	filterHead := container.NewHBox(
		widget.NewLabel("过滤 (Filter)"), layout.NewSpacer(), v.addCondBtn,
	)
	sortHead := container.NewHBox(
		widget.NewLabel("排序 (Sort By)"), layout.NewSpacer(), v.addSortBtn,
	)
	cardBody := container.NewVBox(filterHead, v.filterBox, sortHead, v.sortBox, v.preview)
	cardScroll := container.NewVScroll(cardBody)
	cardScroll.SetMinSize(fyne.NewSize(0, 158))
	cardFooter := container.NewHBox(v.applyBtn, v.resetBtn, layout.NewSpacer(),
		widget.NewLabel("结构化条件 · 方言 SQL 由数据库模块生成"))
	v.builder = container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, cardFooter, nil, nil, cardScroll),
	)

	// 记录操作条：新增 / 删除 / 取消内联编辑 / 提交 / 回退
	v.addRowBtn = widget.NewButton("+ 新增记录", v.addRecord)
	v.deleteRowBtn = widget.NewButton("− 删除记录", v.deleteRecord)
	v.cancelEditBtn = widget.NewButton("✕ 取消编辑", func() { v.grid.cancelCellEdit() })
	v.pendingLabel = widget.NewLabel("待提交 0 项")
	v.commitBtn = widget.NewButton("提交", v.commit)
	v.commitBtn.Importance = widget.HighImportance
	v.revertBtn = widget.NewButton("回退", v.revert)
	v.recordBar = container.NewHBox(
		v.addRowBtn, v.deleteRowBtn, v.cancelEditBtn,
		layout.NewSpacer(),
		v.pendingLabel, v.commitBtn, v.revertBtn,
	)

	// 分页与语句条：参考 Navicat 底部「翻页 / 语句 / 记录计数」
	v.firstBtn = widget.NewButton("|<", func() { v.gotoFirstPage() })
	v.prevBtn = widget.NewButton("◀", func() { v.gotoPage(-1) })
	v.nextBtn = widget.NewButton("▶", func() { v.gotoPage(1) })
	v.pageLabel = widget.NewLabel("1")
	v.statement = widget.NewLabel("")
	v.statement.Truncation = fyne.TextTruncateEllipsis
	v.statement.Wrapping = fyne.TextWrapOff
	v.recordLabel = widget.NewLabel("Record 0 of 0")
	v.pagerBar = container.NewBorder(
		nil, nil,
		container.NewHBox(v.firstBtn, v.prevBtn, v.pageLabel, v.nextBtn),
		container.NewHBox(v.recordLabel),
		v.statement,
	)
	v.infoBar = container.NewHBox(v.info)

	v.addFilterRow()
	v.updatePreview()
	v.updatePendingLabel()
	v.view = container.NewBorder(
		container.NewVBox(v.toolbar, v.simpleBar, v.builder),
		container.NewVBox(widget.NewSeparator(), v.recordBar, v.pagerBar, v.infoBar),
		nil, nil, v.grid.build(),
	)
	return v.view
}

// toggleBuilder 只切换条件卡片本身；开关按钮位于主工具条，收起后依然可见可点。
func (v *dataView) toggleBuilder() {
	v.builderOpen = !v.builderOpen
	showObj(v.builder, v.builderOpen)
	if v.builderOpen {
		v.filterToggle.SetText("过滤 & 排序 ▲")
	} else {
		v.filterToggle.SetText("过滤 & 排序 ▼")
	}
}

// ---------- 过滤 / 排序构建器 ----------

func needsValue(op string) bool {
	return op != "IS NULL" && op != "IS NOT NULL"
}

func (v *dataView) columnNames() []string {
	names := make([]string, 0, len(v.columns))
	for _, c := range v.columns {
		names = append(names, c.Name)
	}
	if len(names) == 0 {
		for _, c := range v.grid.currentPage().Columns {
			names = append(names, c.Name)
		}
	}
	return names
}

func (v *dataView) refreshFieldOptions() {
	options := v.columnNames()
	for _, row := range v.filterRows {
		kept := ""
		if stringIn(row.field.Selected, options) {
			kept = row.field.Selected
		}
		row.field.SetOptions(options)
		if kept != "" {
			row.field.SetSelected(kept)
		} else {
			row.field.ClearSelected()
		}
	}
	for _, row := range v.orderRows {
		kept := ""
		if stringIn(row.field.Selected, options) {
			kept = row.field.Selected
		}
		row.field.SetOptions(options)
		if kept != "" {
			row.field.SetSelected(kept)
		} else {
			row.field.ClearSelected()
		}
	}
	v.updatePreview()
}

func (v *dataView) addFilterRow() {
	row := &conditionRow{
		lead:  widget.NewLabel("WHERE"),
		join:  widget.NewSelect(conditionJoins, nil),
		field: widget.NewSelect(v.columnNames(), nil),
		op:    widget.NewSelect(filterOperators, nil),
		value: widget.NewEntry(),
	}
	row.value.SetPlaceHolder("值；IN 用英文逗号分隔多个值")
	row.op.SetSelected("=")
	row.join.SetSelected("AND")
	row.remove = widget.NewButton("✕", func() { v.removeFilterRow(row) })
	row.box = container.NewHBox(
		row.lead, row.join, row.field, row.op, row.value, row.remove,
	)
	row.field.OnChanged = func(string) { v.updatePreview() }
	row.op.OnChanged = func(op string) {
		if needsValue(op) {
			row.value.Enable()
		} else {
			row.value.Disable()
		}
		v.updatePreview()
	}
	row.value.OnChanged = func(string) { v.updatePreview() }
	row.join.OnChanged = func(string) { v.updatePreview() }
	v.filterRows = append(v.filterRows, row)
	v.filterBox.Add(row.box)
	v.relabelFilterRows()
	v.updatePreview()
}

func (v *dataView) relabelFilterRows() {
	for i, row := range v.filterRows {
		if i == 0 {
			row.lead.SetText("WHERE")
			row.lead.Show()
			row.join.Hide()
		} else {
			row.lead.Hide()
			row.join.Show()
		}
	}
}

func (v *dataView) removeFilterRow(target *conditionRow) {
	for i, row := range v.filterRows {
		if row != target {
			continue
		}
		v.filterBox.Remove(row.box)
		v.filterRows = append(v.filterRows[:i], v.filterRows[i+1:]...)
		break
	}
	v.relabelFilterRows()
	v.updatePreview()
}

func (v *dataView) addOrderRow() {
	row := &orderRow{
		field: widget.NewSelect(v.columnNames(), nil),
		dir:   widget.NewSelect(sortDirections, nil),
	}
	row.dir.SetSelected("升序")
	row.remove = widget.NewButton("✕", func() { v.removeOrderRow(row) })
	row.box = container.NewHBox(
		widget.NewLabel("按"), row.field, row.dir, row.remove,
	)
	row.field.OnChanged = func(string) { v.updatePreview() }
	row.dir.OnChanged = func(string) { v.updatePreview() }
	v.orderRows = append(v.orderRows, row)
	v.sortBox.Add(row.box)
	v.updatePreview()
}

func (v *dataView) removeOrderRow(target *orderRow) {
	for i, row := range v.orderRows {
		if row != target {
			continue
		}
		v.sortBox.Remove(row.box)
		v.orderRows = append(v.orderRows[:i], v.orderRows[i+1:]...)
		break
	}
	v.updatePreview()
}

func (v *dataView) resetConditions() {
	for _, row := range v.filterRows {
		v.filterBox.Remove(row.box)
	}
	for _, row := range v.orderRows {
		v.sortBox.Remove(row.box)
	}
	v.filterRows = nil
	v.orderRows = nil
	v.page = 0
	v.addFilterRow()
	v.updatePreview()
}

func (v *dataView) conditions() []domain.FilterCondition {
	out := make([]domain.FilterCondition, 0, len(v.filterRows))
	for _, row := range v.filterRows {
		if row.field.Selected == "" || row.op.Selected == "" {
			continue
		}
		value := strings.TrimSpace(row.value.Text)
		if needsValue(row.op.Selected) && value == "" {
			continue
		}
		c := domain.FilterCondition{
			Column: row.field.Selected, Operator: row.op.Selected, Value: row.value.Text,
		}
		if len(out) > 0 {
			c.Join = row.join.Selected
			if c.Join == "" {
				c.Join = "AND"
			}
		}
		out = append(out, c)
	}
	return out
}

func (v *dataView) orderTerms() []domain.SortTerm {
	out := make([]domain.SortTerm, 0, len(v.orderRows))
	for _, row := range v.orderRows {
		if row.field.Selected == "" {
			continue
		}
		out = append(out, domain.SortTerm{
			Column: row.field.Selected, Desc: row.dir.Selected == "降序",
		})
	}
	return out
}

func (v *dataView) updatePreview() {
	whereParts := make([]string, 0, len(v.filterRows))
	for i, c := range v.conditions() {
		term := c.Column + " " + c.Operator
		if needsValue(c.Operator) {
			term += " '" + strings.TrimSpace(c.Value) + "'"
		}
		if i > 0 {
			term = c.Join + " " + term
		}
		whereParts = append(whereParts, term)
	}
	orderParts := make([]string, 0, len(v.orderRows))
	for _, t := range v.orderTerms() {
		direction := "ASC"
		if t.Desc {
			direction = "DESC"
		}
		orderParts = append(orderParts, t.Column+" "+direction)
	}
	text := "未设置条件（显示全部数据）"
	if len(whereParts) > 0 {
		text = "WHERE " + strings.Join(whereParts, " ")
	}
	if len(orderParts) > 0 {
		text += "\nORDER BY " + strings.Join(orderParts, ", ")
	}
	text += "\n条件为结构化描述；实际 SQL 由数据库模块按方言生成（自动引用标识符、转义字面量）"
	v.preview.SetText(text)
}

// ---------- 分类与数据加载 ----------

func (v *dataView) setCategory(cat dbCategory, caps domain.CapabilitySet) {
	v.category = cat
	v.caps = caps
	relational := cat == categoryRelational

	showObj(v.simpleBar, !relational)
	showObj(v.builder, relational && v.builderOpen)
	showObj(v.filterToggle, relational)
	showObj(v.recordBar, relational && caps.Has(domain.CapDataEdit))
	showObj(v.pageSizeBox, relational)
	showObj(v.pagerBar, relational)
	v.grid.editable = relational && caps.Has(domain.CapDataEdit)
	v.grid.showTypes = relational
	showObj(v.editBtn, caps.Has(domain.CapDataEdit))
	showObj(v.importBtn, caps.Has(domain.CapDataEdit))
	showObj(v.truncateBtn, caps.Has(domain.CapTruncateTable))
	showObj(v.emptyBtn, caps.Has(domain.CapEmptyTable))
	showObj(v.exportBtn, cat != categoryKeyValue)
	showObj(v.sort, cat != categoryKeyValue)
	showObj(v.sortLabel, cat != categoryKeyValue)

	switch cat {
	case categoryDocument:
		v.filterLabel.SetText("过滤 (Filter JSON)")
		v.sortLabel.SetText("排序 (Sort JSON)")
		v.filter.SetPlaceHolder(`filter JSON，如 {"age":{"$gt":18}}`)
		v.sort.SetPlaceHolder(`sort JSON，如 {"age":-1}`)
		v.refreshBtn.SetText("刷新数据")
	case categoryKeyValue:
		v.filterLabel.SetText("键匹配")
		v.filter.SetPlaceHolder("键匹配模式，如 user:*（留空为 *）")
		v.sort.SetText("")
		v.refreshBtn.SetText("刷新键列表")
	default:
		v.refreshBtn.SetText("刷新")
	}
	if v.table == "" {
		v.info.SetText("请在左侧选择一个" + cat.objectName())
	}
}

// setTarget 只同步目标对象，不触发数据加载（右键动作使用）。
func (v *dataView) setTarget(conn, database, table string) {
	v.conn, v.database, v.table = conn, database, table
	v.caps = v.u.capsFor(conn)
	v.category = categoryOf(v.caps)
}

func (v *dataView) load(conn, database, table string) {
	v.conn, v.database, v.table = conn, database, table
	v.caps = v.u.capsFor(conn)
	v.setCategory(categoryOf(v.caps), v.caps)
	v.filter.SetText("")
	v.sort.SetText("")
	v.page = 0
	v.hasNext = false
	v.clearStaged()
	v.resetConditions()
	v.grid.reset()
	if table == "" && v.category != categoryKeyValue {
		v.info.SetText("请在左侧选择一个" + v.category.objectName())
		return
	}
	v.loadMetadata()
	v.reload()
}

// loadMetadata 读取字段列表（供过滤/排序下拉使用）与主键列（供单元格回写定位使用）。
func (v *dataView) loadMetadata() {
	if v.category != categoryRelational || v.table == "" || v.conn == "" {
		v.columns = nil
		v.keyColumns = nil
		return
	}
	conn := v.conn
	ref := domain.ObjectRef{Database: v.database, Name: v.table}
	v.u.async(func() {
		schema, err := v.u.core.Describe(context.Background(), conn, ref)
		keys, keyErr := v.u.core.TableKeyColumns(context.Background(), conn, ref)
		fyne.Do(func() {
			if v.table != ref.Name || v.database != ref.Database {
				return
			}
			if err == nil {
				v.columns = schema.Columns
				v.refreshFieldOptions()
			}
			if keyErr == nil {
				v.keyColumns = keys
			}
		})
	})
}

func (v *dataView) pageSize() int {
	n, err := strconv.Atoi(v.pageSizeSel.Selected)
	if err != nil || n <= 0 {
		return defaultPreviewLimit
	}
	return n
}

func (v *dataView) gotoPage(delta int) {
	if delta > 0 && !v.hasNext {
		return
	}
	v.page += delta
	if v.page < 0 {
		v.page = 0
	}
	v.gotoFirstPageOrReload()
}

func (v *dataView) gotoFirstPage() {
	if v.page == 0 {
		return
	}
	v.page = 0
	v.gotoFirstPageOrReload()
}

func (v *dataView) gotoFirstPageOrReload() {
	v.grid.finishCellEdit(true)
	v.clearStaged()
	v.reload()
}

// reload 使用 Query.Ref 让各数据库模块自行生成预览语句，GUI 不拼接任何方言。
func (v *dataView) reload() {
	if v.conn == "" || (v.table == "" && v.category != categoryKeyValue) {
		v.u.showInfo("提示", "请先在左侧选择一个"+v.category.objectName())
		return
	}
	size := v.pageSize()
	q := domain.Query{Database: v.database, MaxRows: size}
	if v.table != "" {
		q.Ref = domain.ObjectRef{Database: v.database, Name: v.table}
	}
	switch v.category {
	case categoryRelational:
		q.Offset = v.page * size
		q.Conditions = v.conditions()
		q.OrderTerms = v.orderTerms()
	case categoryKeyValue:
		q.Filter = strings.TrimSpace(v.filter.Text)
		if q.Filter == "" && v.table == "" {
			q.Filter = "*"
		}
	default:
		q.Filter = strings.TrimSpace(v.filter.Text)
		q.Sort = strings.TrimSpace(v.sort.Text)
	}
	v.grid.cancelCellEdit()
	v.clearStaged()
	v.grid.reset()
	label := v.tableLabel(q)
	v.info.SetText(label)
	switch v.category {
	case categoryRelational:
		v.statement.SetText(statementPreview(v.database, v.table, q, size))
	case categoryKeyValue:
		v.statement.SetText("SCAN / 键匹配：" + v.tableLabel(q))
	default:
		v.statement.SetText("MongoDB filter / sort · Extended JSON · " + v.tableLabel(q))
	}
	sink := &gridSink{grid: v.grid}
	v.u.async(func() {
		out, err := v.u.core.Execute(context.Background(), v.conn, q, sink)
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() {
			rows := int(out.Summary.Rows)
			v.hasNext = rows >= size
			v.rowCount = rows
			v.updatePager(rows, out.Summary.DurationMs, label)
			// 覆盖 resultGrid.finish 中「已达行数上限」的提示：满页属于正常分页。
			v.grid.status.SetText(fmt.Sprintf("第 %d 页 · %d 行 · %d ms · 每页 %d 行",
				v.page+1, rows, out.Summary.DurationMs, size))
		})
	})
}

func (v *dataView) updatePager(rows int, elapsed int64, label string) {
	v.pageLabel.SetText(fmt.Sprintf("第 %d 页", v.page+1))
	if v.page > 0 {
		v.prevBtn.Enable()
		v.firstBtn.Enable()
	} else {
		v.prevBtn.Disable()
		v.firstBtn.Disable()
	}
	if v.hasNext {
		v.nextBtn.Enable()
	} else {
		v.nextBtn.Disable()
	}
	v.updateRecordLabel()
	// 行数 / 耗时 / 每页 已由网格底部状态条展示，这里只保留查询对象标签，避免重复堆叠。
	v.info.SetText(label)
}

func (v *dataView) updateRecordLabel() {
	if v.selectedRow >= 0 && v.selectedRow < v.rowCount {
		v.recordLabel.SetText(fmt.Sprintf("Record %d of %d", v.selectedRow+1, v.rowCount))
	} else {
		v.recordLabel.SetText(fmt.Sprintf("Record 1 of %d", v.rowCount))
	}
}

// statementPreview 是给用户看的条件描述；真正执行的方言 SQL 由各 provider 生成。
func statementPreview(database, table string, q domain.Query, size int) string {
	target := table
	if database != "" {
		target = database + "." + table
	}
	parts := []string{"SELECT * FROM " + target}
	where := make([]string, 0, len(q.Conditions))
	for i, c := range q.Conditions {
		term := c.Column + " " + c.Operator
		if c.Value != "" {
			term += " '" + c.Value + "'"
		}
		if i > 0 && c.Join != "" {
			term = c.Join + " " + term
		}
		where = append(where, term)
	}
	if len(where) > 0 {
		parts = append(parts, "WHERE "+strings.Join(where, " "))
	}
	order := make([]string, 0, len(q.OrderTerms))
	for _, t := range q.OrderTerms {
		if t.Desc {
			order = append(order, t.Column+" DESC")
		} else {
			order = append(order, t.Column+" ASC")
		}
	}
	if len(order) > 0 {
		parts = append(parts, "ORDER BY "+strings.Join(order, ", "))
	}
	parts = append(parts, fmt.Sprintf("LIMIT %d,%d", q.Offset, size))
	return strings.Join(parts, " ") + "  ·  条件描述（实际方言由数据库模块生成）"
}

func (v *dataView) tableLabel(q domain.Query) string {
	name := v.database + "." + v.table
	if v.table == "" {
		name = v.database + " 的键"
	}
	return name
}

// ---------- 原单元格内联编辑 / 记录暂存 ----------

// cellValue 返回某单元格「当前值」：新增行取暂存值，原有行取暂存值或原始值。
func (v *dataView) cellValue(id widget.TableCellID) string {
	page := v.grid.currentPage()
	if id.Row <= 0 || id.Col >= len(page.Columns) {
		return ""
	}
	row := id.Row - 1
	if row >= len(page.Rows) {
		nr := row - len(page.Rows)
		if nr < 0 || nr >= len(v.newRows) || id.Col >= len(v.newRows[nr]) {
			return ""
		}
		return v.newRows[nr][id.Col]
	}
	if value, ok := v.pending[cellKey{row, id.Col}]; ok {
		return value
	}
	if id.Col < len(page.Rows[row]) {
		return cellText(page.Rows[row][id.Col].Display())
	}
	return ""
}

// cellDisplay 给网格提供带状态标记的显示文本；表头交回 resultGrid 追加列类型。
func (v *dataView) cellDisplay(id widget.TableCellID) (string, bool) {
	if v.category != categoryRelational || id.Row == 0 {
		return "", false
	}
	page := v.grid.currentPage()
	if len(page.Columns) == 0 || id.Col >= len(page.Columns) {
		return "", false
	}
	row := id.Row - 1
	value := v.cellValue(id)
	if row >= len(page.Rows) {
		return value, true
	}
	key := cellKey{row, id.Col}
	if v.pendingDeletes[row] {
		return "✕ " + value, true
	}
	if _, ok := v.pending[key]; ok {
		return "✎ " + value, true
	}
	return "", false
}

// stageCell 由内联 Entry 回车或切换单元格触发，写入暂存；改回原值则自动撤销。
func (v *dataView) stageCell(id widget.TableCellID, text string) {
	if v.category != categoryRelational || id.Row <= 0 {
		return
	}
	page := v.grid.currentPage()
	if id.Col >= len(page.Columns) {
		return
	}
	row := id.Row - 1
	if row >= len(page.Rows) {
		nr := row - len(page.Rows)
		if nr < 0 || nr >= len(v.newRows) {
			return
		}
		v.newRows[nr][id.Col] = text
	} else {
		original := ""
		if id.Col < len(page.Rows[row]) {
			original = cellText(page.Rows[row][id.Col].Display())
		}
		key := cellKey{row, id.Col}
		if text == original {
			delete(v.pending, key)
		} else {
			v.pending[key] = text
		}
	}
	v.updatePendingLabel()
}

func (v *dataView) addRecord() {
	page := v.grid.currentPage()
	if len(page.Columns) == 0 {
		v.u.showInfo("提示", "请先加载表数据，再新增记录")
		return
	}
	v.grid.finishCellEdit(true)
	v.newRows = append(v.newRows, make([]string, len(page.Columns)))
	v.updatePendingLabel()
	v.grid.refresh()
	id := widget.TableCellID{Row: len(page.Rows) + len(v.newRows), Col: 0}
	if v.grid.active < len(v.grid.tables) {
		v.grid.tables[v.grid.active].ScrollTo(id)
	}
	v.grid.startEditing(id)
	v.u.setStatus("已在网格末尾新增一行，逐格输入后点「提交」")
}

func (v *dataView) deleteRecord() {
	if v.grid.selected.Row <= 0 {
		v.u.showInfo("提示", "请先在数据网格中选中要删除的记录")
		return
	}
	v.grid.finishCellEdit(true)
	page := v.grid.currentPage()
	row := v.grid.selected.Row - 1
	if row >= len(page.Rows) {
		nr := row - len(page.Rows)
		if nr < 0 || nr >= len(v.newRows) {
			return
		}
		v.newRows = append(v.newRows[:nr], v.newRows[nr+1:]...)
	} else {
		if v.pendingDeletes[row] {
			delete(v.pendingDeletes, row)
		} else {
			v.pendingDeletes[row] = true
		}
	}
	v.updatePendingLabel()
	v.grid.refresh()
}

func (v *dataView) stagedCount() int {
	return len(v.pending) + len(v.newRows) + len(v.pendingDeletes)
}

func (v *dataView) updatePendingLabel() {
	n := v.stagedCount()
	v.pendingLabel.SetText(fmt.Sprintf("待提交 %d 项 · 改 %d / 增 %d / 删 %d",
		n, len(v.pending), len(v.newRows), len(v.pendingDeletes)))
	if n > 0 {
		v.commitBtn.Enable()
		v.revertBtn.Enable()
	} else {
		v.commitBtn.Disable()
		v.revertBtn.Disable()
	}
}

func (v *dataView) clearStaged() {
	v.grid.cancelCellEdit()
	v.pending = make(map[cellKey]string)
	v.newRows = nil
	v.pendingDeletes = make(map[int]bool)
	v.updatePendingLabel()
}

func (v *dataView) revert() {
	v.clearStaged()
	v.grid.refresh()
	v.u.setStatus("已回退全部未提交的记录变更")
}

// rowKey 用原始行的键列值构造定位条件，键列由元数据推断，推断失败时退化为整行匹配。
func rowKey(page domain.ResultPage, row int, keys []string) map[string]*string {
	key := make(map[string]*string, len(keys))
	if row < 0 || row >= len(page.Rows) {
		return key
	}
	for _, name := range keys {
		index := -1
		for i, col := range page.Columns {
			if col.Name == name {
				index = i
				break
			}
		}
		if index < 0 || index >= len(page.Rows[row]) {
			continue
		}
		original := cellText(page.Rows[row][index].Display())
		if original == "NULL" {
			key[name] = nil
		} else {
			value := original
			key[name] = &value
		}
	}
	return key
}

func (v *dataView) commit() {
	v.grid.finishCellEdit(true)
	if v.stagedCount() == 0 {
		v.u.showInfo("提示", "没有待提交的记录变更")
		return
	}
	page := v.grid.currentPage()
	if len(page.Columns) == 0 {
		v.u.showInfo("提示", "当前没有可编辑的数据")
		return
	}
	keys := append([]string(nil), v.keyColumns...)
	if len(keys) == 0 {
		for _, col := range page.Columns {
			keys = append(keys, col.Name)
		}
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}

	edits := make([]domain.RowEdit, 0, v.stagedCount())

	// 已有行的单元格修改：按行聚合成一条 UPDATE。
	byRow := make(map[int]map[string]*string)
	for cell, text := range v.pending {
		if cell.row >= len(page.Rows) || cell.col >= len(page.Columns) || v.pendingDeletes[cell.row] {
			continue
		}
		name := page.Columns[cell.col].Name
		if keySet[name] && len(v.keyColumns) > 0 {
			continue // 键列用作定位条件，不在内联编辑器中修改
		}
		if _, ok := byRow[cell.row]; !ok {
			byRow[cell.row] = make(map[string]*string)
		}
		if text == "NULL" {
			byRow[cell.row][name] = nil
		} else {
			value := text
			byRow[cell.row][name] = &value
		}
	}
	rows := make([]int, 0, len(byRow))
	for row := range byRow {
		rows = append(rows, row)
	}
	sort.Ints(rows)
	for _, row := range rows {
		key := rowKey(page, row, keys)
		if len(key) == 0 {
			v.u.showInfo("提示", "无法确定定位行的键列，请改用「编辑选中行」")
			return
		}
		edits = append(edits, domain.RowEdit{
			Kind: domain.EditUpdate, Key: key, Values: byRow[row],
		})
	}

	// 删除标记。
	deleteRows := make([]int, 0, len(v.pendingDeletes))
	for row := range v.pendingDeletes {
		deleteRows = append(deleteRows, row)
	}
	sort.Ints(deleteRows)
	for _, row := range deleteRows {
		key := rowKey(page, row, keys)
		if len(key) == 0 {
			v.u.showInfo("提示", "无法确定待删除行的键列")
			return
		}
		edits = append(edits, domain.RowEdit{Kind: domain.EditDelete, Key: key})
	}

	// 新增行：逐格填写的值组装成 INSERT，空值列交给数据库默认值。
	for _, values := range v.newRows {
		insert := make(map[string]*string, len(values))
		for i, text := range values {
			if i >= len(page.Columns) || text == "" {
				continue
			}
			name := page.Columns[i].Name
			if text == "NULL" {
				insert[name] = nil
			} else {
				value := text
				insert[name] = &value
			}
		}
		if len(insert) > 0 {
			edits = append(edits, domain.RowEdit{Kind: domain.EditInsert, Values: insert})
		}
	}
	if len(edits) == 0 {
		v.u.showInfo("提示", "没有可提交的变更；键列请使用「编辑选中行」修改")
		return
	}

	req := domain.EditRequest{
		Ref:         domain.ObjectRef{Database: v.database, Name: v.table},
		KeyColumns:  keys,
		Edits:       edits,
		Transaction: true,
	}
	conn := v.conn
	v.u.async(func() {
		previews, err := v.u.core.PreviewDataEdit(context.Background(), conn, req)
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() {
			dialog.NewConfirm("确认提交记录变更",
				strings.Join(previews, "\n---\n"), func(ok bool) {
					if !ok {
						return
					}
					v.u.async(func() {
						result, err := v.u.core.ApplyDataEdit(context.Background(), conn, req)
						fyne.Do(func() {
							if err != nil {
								v.u.showError(err)
								return
							}
							v.u.showInfo("完成", fmt.Sprintf("已提交 %d 条变更", result.Applied))
							v.clearStaged()
							v.reload()
						})
					})
				}, v.u.win).Show()
		})
	})
}

// ---------- 表级动作 ----------

func (v *dataView) showTableAction(action domain.TableAction) {
	v.showTableActionFor(action, v.conn, v.database, v.table)
}

// showTableActionFor 生成 TRUNCATE / DELETE FROM 脚本并交给统一确认对话框。
func (v *dataView) showTableActionFor(action domain.TableAction, conn, database, table string) {
	if table == "" || conn == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	title := "TRUNCATE TABLE"
	message := "即将生成 TRUNCATE 脚本，该操作会清空整张表且不可回滚。是否继续？"
	if action == domain.TableActionEmpty {
		title = "清空表数据 (DELETE FROM)"
		message = "即将生成 DELETE FROM 脚本，该操作会逐行删除全部数据且不可自动回滚。是否继续？"
	}
	dialog.ShowConfirm(title, message, func(ok bool) {
		if !ok {
			return
		}
		ref := domain.ObjectRef{Database: database, Name: table}
		v.u.async(func() {
			script, err := v.u.core.PreviewTableAction(context.Background(), conn, ref, action)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialog(title+" 预览", conn, script) })
		})
	}, v.u.win)
}
