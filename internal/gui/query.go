//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	core "github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/export"
)

const (
	labelSQL            = "SQL"
	labelMongoFilter    = "Mongo 过滤"
	labelMongoPipeline  = "Mongo 聚合"
	labelRedis          = "Redis 命令"
	defaultPreviewLimit = 100
)

// capsFor 从连接定义查询模块能力，不建立真实连接（能力驱动菜单，通用层不判断 Kind）。
func (u *ui) capsFor(conn string) domain.CapabilitySet {
	list, err := u.core.ListConnections()
	if err != nil {
		return nil
	}
	for _, c := range list {
		if c.Name == conn || c.ID == conn {
			caps, err := core.KindCapabilities(c.Kind)
			if err == nil {
				return caps
			}
		}
	}
	return nil
}

func languageLabel(lang string) string {
	switch domain.QueryLanguage(lang) {
	case domain.LangMongoFilter:
		return labelMongoFilter
	case domain.LangMongoPipeline:
		return labelMongoPipeline
	case domain.LangRedis:
		return labelRedis
	default:
		return labelSQL
	}
}

func stringIn(value string, options []string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

// resultGrid 是可复用的流式结果网格：每个结果集一个页签，文档型结果集另附 JSON 树页签。
type resultGrid struct {
	u           *ui
	mu          sync.RWMutex
	pages       []domain.ResultPage
	tabs        *container.AppTabs
	tableTabs   []*container.TabItem
	tables      []*widget.Table
	widthsSized []bool
	active      int
	status      *widget.Label

	selected     widget.TableCellID
	hasSelection bool
	// onCellSelected 在单击数据单元格时通知使用方（表数据页据此定位记录）。
	onCellSelected func(widget.TableCellID)

	// 内联编辑：editableCell 模板在选中单元格内直接展开 Entry。
	editable     bool
	editing      bool
	editingID    widget.TableCellID
	editingIndex int
	editingCell  *editableCell
	showTypes    bool
	onActivated  func(widget.TableCellID)
	onCommitted  func(widget.TableCellID, string)
	cellDisplay  func(widget.TableCellID) (string, bool)
	editValue    func(widget.TableCellID) string
	extraRows    func() int
}

func newResultGrid(u *ui) *resultGrid {
	g := &resultGrid{u: u}
	g.status = widget.NewLabel("尚无结果")
	g.tabs = container.NewAppTabs()
	g.tabs.OnSelected = func(item *container.TabItem) {
		for i, tab := range g.tableTabs {
			if tab == item {
				g.active = i
				return
			}
		}
	}
	g.reset()
	return g
}

func (g *resultGrid) newTable(index int) *widget.Table {
	table := widget.NewTable(
		func() (int, int) {
			g.mu.RLock()
			defer g.mu.RUnlock()
			if index >= len(g.pages) {
				return 0, 0
			}
			page := g.pages[index]
			rows := len(page.Rows) + 1
			if g.extraRows != nil {
				rows += g.extraRows()
			}
			return rows, len(page.Columns)
		},
		func() fyne.CanvasObject {
			c := newEditableCell()
			c.OnTap = func(id widget.TableCellID) { g.handleCellTap(index, id) }
			c.OnSubmit = func(id widget.TableCellID, text string) {
				if g.onCommitted != nil {
					g.onCommitted(id, text)
				}
				g.finishCellEdit(false)
			}
			return c
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			cell, ok := o.(*editableCell)
			if !ok {
				return
			}
			cell.ID = id
			text := g.displayCell(index, id)
			if g.editable && g.editing && index == g.editingIndex && g.editingID == id && id.Row > 0 {
				initial := text
				if g.editValue != nil {
					initial = g.editValue(id)
				}
				cell.ShowEditor(initial)
				g.editingCell = cell
			} else {
				cell.ShowLabel(text)
			}
		},
	)
	table.OnSelected = func(id widget.TableCellID) {
		g.mu.Lock()
		g.selected = id
		g.hasSelection = id.Row > 0
		g.mu.Unlock()
	}
	return table
}

// displayCell 允许表数据页覆盖单元格显示（暂存值、新增行、删除标记），
// 未覆盖时回落到结果集原始数据；表头可附加列类型，与 Navicat 风格一致。
func (g *resultGrid) displayCell(index int, id widget.TableCellID) string {
	if g.cellDisplay != nil {
		if text, ok := g.cellDisplay(id); ok {
			return text
		}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if index >= len(g.pages) {
		return ""
	}
	page := g.pages[index]
	if id.Row == 0 {
		if id.Col >= len(page.Columns) {
			return ""
		}
		col := page.Columns[id.Col]
		if g.showTypes && col.Type != "" {
			return col.Name + "\n" + col.Type
		}
		return col.Name
	}
	row := id.Row - 1
	if row >= len(page.Rows) || id.Col >= len(page.Rows[row]) {
		return ""
	}
	return cellText(page.Rows[row][id.Col].Display())
}

// handleCellTap 由单元格模板接管单击：提交上一个编辑、同步选中行、激活内联编辑。
func (g *resultGrid) handleCellTap(index int, id widget.TableCellID) {
	if g.editable {
		g.finishCellEdit(true)
	}
	if index < len(g.tables) {
		g.tables[index].Select(id)
		if !g.editable {
			// 非编辑网格（SQL 查询页）沿用 Table 键盘导航所需的焦点。
			if canvas := fyne.CurrentApp().Driver().CanvasForObject(g.tables[index]); canvas != nil {
				canvas.Focus(g.tables[index])
			}
		}
	}
	g.mu.Lock()
	g.selected = id
	g.hasSelection = id.Row > 0
	g.mu.Unlock()
	if g.onCellSelected != nil {
		g.onCellSelected(id)
	}
	if g.editable && id.Row > 0 && g.onActivated != nil {
		g.onActivated(id)
	}
}

func (g *resultGrid) startEditing(id widget.TableCellID) {
	g.finishCellEdit(true)
	if len(g.tables) == 0 || g.active >= len(g.tables) {
		return
	}
	g.editing = true
	g.editingIndex = g.active
	g.editingID = id
	g.editingCell = nil
	g.tables[g.editingIndex].Refresh()
	if g.editingCell != nil {
		g.editingCell.Focus(fyne.CurrentApp().Driver().CanvasForObject(g.tables[g.editingIndex]))
	}
}

// refresh 在行数变化（新增/删除待提交记录）后重建可见单元格。
func (g *resultGrid) refresh() {
	for _, table := range g.tables {
		table.Refresh()
	}
}

// finishCellEdit 结束内联编辑；commit=true 时把输入框内容交给使用方暂存。
func (g *resultGrid) finishCellEdit(commit bool) {
	if !g.editing {
		return
	}
	id := g.editingID
	index := g.editingIndex
	value := ""
	if g.editingCell != nil {
		value = g.editingCell.Entry.Text
	}
	g.editing = false
	g.editingCell = nil
	if commit && g.onCommitted != nil {
		g.onCommitted(id, value)
	}
	if index < len(g.tables) {
		g.tables[index].Refresh()
	}
}

func (g *resultGrid) cancelCellEdit() { g.finishCellEdit(false) }

func (g *resultGrid) build() fyne.CanvasObject {
	return container.NewBorder(nil, g.status, nil, nil, g.tabs)
}

func (g *resultGrid) reset() {
	g.mu.Lock()
	g.pages = nil
	g.hasSelection = false
	g.mu.Unlock()
	g.editing = false
	g.editingCell = nil
	g.active = 0
	g.tableTabs = nil
	g.tables = nil
	g.widthsSized = nil
	g.tabs.SetItems([]*container.TabItem{
		container.NewTabItem("结果", widget.NewLabel("执行查询后在此显示结果")),
	})
	g.status.SetText("正在执行 …")
}

func (g *resultGrid) startPage(index int, cols []domain.Column) {
	g.mu.Lock()
	for len(g.pages) <= index {
		g.pages = append(g.pages, domain.ResultPage{})
	}
	g.pages[index] = domain.ResultPage{Columns: cols}
	g.mu.Unlock()

	for len(g.tables) <= index {
		i := len(g.tables)
		table := g.newTable(i)
		tab := container.NewTabItem(fmt.Sprintf("结果集 %d", i+1), table)
		g.tables = append(g.tables, table)
		g.tableTabs = append(g.tableTabs, tab)
		if i == 0 {
			g.tabs.SetItems([]*container.TabItem{tab})
		} else {
			g.tabs.Append(tab)
		}
	}
	headers := make([]string, len(cols))
	for i, col := range cols {
		headers[i] = col.Name
	}
	sizeTableColumns(g.tables[index], headers, nil)
	for len(g.widthsSized) <= index {
		g.widthsSized = append(g.widthsSized, false)
	}
	g.widthsSized[index] = false
	g.tables[index].Refresh()
}

func (g *resultGrid) appendRows(index int, rows []domain.Row) {
	g.mu.Lock()
	for len(g.pages) <= index {
		g.pages = append(g.pages, domain.ResultPage{})
	}
	g.pages[index].Rows = append(g.pages[index].Rows, rows...)
	g.mu.Unlock()
	if index < len(g.tables) {
		if index < len(g.widthsSized) && !g.widthsSized[index] && len(rows) > 0 {
			g.sizeColumnsToRows(index, rows)
			g.widthsSized[index] = true
		}
		g.tables[index].Refresh()
	}
}

// sizeColumnsToRows 用第一批数据样本调整列宽，之后不再变动，避免列宽抖动。
func (g *resultGrid) sizeColumnsToRows(index int, rows []domain.Row) {
	g.mu.RLock()
	headers := make([]string, 0)
	if index < len(g.pages) {
		for _, col := range g.pages[index].Columns {
			headers = append(headers, col.Name)
		}
	}
	g.mu.RUnlock()

	sample := make([][]string, 0, maxSampleRows)
	for _, row := range rows {
		if len(sample) >= maxSampleRows {
			break
		}
		line := make([]string, len(row))
		for i, cell := range row {
			line[i] = cellText(cell.Display())
		}
		sample = append(sample, line)
	}
	sizeTableColumns(g.tables[index], headers, sample)
}

func (g *resultGrid) finish(outcome domain.QueryOutcome) {
	g.mu.Lock()
	for i := range g.pages {
		if i < len(outcome.Pages) {
			g.pages[i].JSONDocuments = outcome.Pages[i].JSONDocuments
		}
	}
	g.mu.Unlock()

	for i, page := range outcome.Pages {
		if len(page.JSONDocuments) == 0 {
			continue
		}
		g.tabs.Append(container.NewTabItem(
			fmt.Sprintf("JSON %d", i+1), newJSONTree(page.JSONDocuments)))
	}

	extra := ""
	if len(outcome.Pages) > 1 {
		extra = fmt.Sprintf(" · 共 %d 个结果集，可用页签切换", len(outcome.Pages))
	}
	if outcome.Summary.Truncated {
		extra += " · 已达行数上限，结果被截断"
	}
	g.status.SetText(fmt.Sprintf("%d 行 · %d ms%s", outcome.Summary.Rows,
		outcome.Summary.DurationMs, extra))
}

func (g *resultGrid) currentPage() domain.ResultPage {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.pages) == 0 {
		return domain.ResultPage{}
	}
	index := g.active
	if index < 0 || index >= len(g.pages) {
		index = 0
	}
	return g.pages[index]
}

// selection 返回当前活动结果集中选中的数据行下标（不含表头行）。
func (g *resultGrid) selection() (domain.ResultPage, int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.hasSelection || g.selected.Row <= 0 || len(g.pages) == 0 {
		return domain.ResultPage{}, 0, false
	}
	index := g.active
	if index >= len(g.pages) {
		return domain.ResultPage{}, 0, false
	}
	page := g.pages[index]
	row := g.selected.Row - 1
	if row >= len(page.Rows) {
		return domain.ResultPage{}, 0, false
	}
	return page, row, true
}

// gridSink 把后台 goroutine 的流式结果通过 fyne.Do 切回主线程渲染。
type gridSink struct{ grid *resultGrid }

func (s *gridSink) OnResultStart(index int, cols []domain.Column) {
	fyne.Do(func() { s.grid.startPage(index, cols) })
}

func (s *gridSink) OnRows(index int, rows []domain.Row) {
	fyne.Do(func() { s.grid.appendRows(index, rows) })
}

func (s *gridSink) OnFinish(outcome domain.QueryOutcome) {
	fyne.Do(func() { s.grid.finish(outcome) })
}

type queryView struct {
	u *ui

	view        fyne.CanvasObject
	editor      *widget.Entry
	conn        *widget.Select
	dbEntry     *widget.Entry
	collection  *widget.Entry
	language    *widget.Select
	connLabel   *widget.Label
	dbLabel     *widget.Label
	langLabel   *widget.Label
	collLabel   *widget.Label
	grid        *resultGrid
	runBtn      *widget.Button
	explainBtn  *widget.Button
	cancelBtn   *widget.Button
	exportBtn   *widget.Button
	favBtn      *widget.Button
	libBtn      *widget.Button
	formatBtn   *widget.Button
	templateBtn *widget.Button
	newQueryBtn *widget.Button
	gutter      *widget.Label
	cancel      context.CancelFunc
	caps        domain.CapabilitySet
	category    dbCategory
}

func newQueryView(u *ui) *queryView { return &queryView{u: u} }

func (v *queryView) build() fyne.CanvasObject {
	v.editor = widget.NewMultiLineEntry()
	v.editor.Wrapping = fyne.TextWrapOff
	v.editor.SetPlaceHolder("输入 SQL 或 MongoDB filter/pipeline JSON；可选中部分文本执行")
	v.editor.SetText("SELECT VERSION();")

	v.conn = widget.NewSelect([]string{}, func(name string) { v.applyCapabilities(name) })
	v.dbEntry = widget.NewEntry()
	v.dbEntry.SetPlaceHolder("当前数据库（可选）")
	v.collection = widget.NewEntry()
	v.collection.SetPlaceHolder("MongoDB 集合（可选）")
	v.language = widget.NewSelect([]string{labelSQL}, nil)
	v.language.SetSelected(labelSQL)
	v.grid = newResultGrid(v.u)

	v.runBtn = widget.NewButton("▶ 执行", func() { v.run(false) })
	v.runBtn.Importance = widget.HighImportance
	v.explainBtn = widget.NewButton("执行计划", func() { v.run(true) })
	v.cancelBtn = widget.NewButton("停止", v.cancelQuery)
	v.cancelBtn.Disable()
	v.exportBtn = widget.NewButton("导出 CSV", func() { exportPage(v.u, v.grid.currentPage()) })
	v.favBtn = widget.NewButton("保存", func() {
		showSaveFavorite(v.u, v.conn.Selected, v.dbEntry.Text, v.collection.Text,
			string(v.currentLanguage()), v.currentText())
	})
	v.formatBtn = widget.NewButton("格式化 SQL", func() {
		v.editor.SetText(formatSQL(v.editor.Text))
	})
	v.templateBtn = widget.NewButton("查询模板", func() {
		table := v.u.currentTable
		if table == "" {
			table = "<表名>"
		}
		v.editor.SetText("SELECT *\nFROM " + table + "\nLIMIT 100;")
	})
	v.newQueryBtn = widget.NewButton("+ 新查询", func() { v.u.openNewQuery() })
	v.libBtn = widget.NewButton("代码片段", func() {
		showQueryLibrary(v.u, func(pick queryPick) {
			v.editor.SetText(pick.Text)
			if pick.Connection != "" {
				v.conn.SetSelected(pick.Connection)
			}
			if pick.Database != "" {
				v.dbEntry.SetText(pick.Database)
			}
			if pick.Collection != "" {
				v.collection.SetText(pick.Collection)
			}
			if pick.Language != "" {
				v.language.SetSelected(languageLabel(pick.Language))
			}
		})
	})

	v.connLabel = widget.NewLabel("连接")
	v.dbLabel = widget.NewLabel("数据库")
	v.langLabel = widget.NewLabel("语言")
	v.collLabel = widget.NewLabel("集合")

	// 参考 Navicat 查询页：第一行文档工具，第二行连接 / 执行控制，编辑区带行号槽。
	docBar := container.NewHBox(
		v.favBtn, v.formatBtn, v.templateBtn, v.libBtn,
		widget.NewSeparator(), v.exportBtn, v.newQueryBtn,
		layout.NewSpacer(),
	)
	runBar := container.NewHBox(
		v.connLabel, v.conn, v.dbLabel, v.dbEntry,
		v.langLabel, v.language, v.collLabel, v.collection,
		widget.NewSeparator(), v.runBtn, v.cancelBtn, v.explainBtn,
	)
	v.gutter = widget.NewLabel("1")
	v.gutter.Wrapping = fyne.TextWrapOff
	v.editor.OnChanged = func(text string) { v.gutter.SetText(lineNumberGutter(text)) }
	v.gutter.SetText(lineNumberGutter(v.editor.Text))

	editorPane := container.NewBorder(
		container.NewVBox(docBar, runBar, widget.NewSeparator()),
		nil, container.NewVBox(v.gutter), nil, v.editor,
	)
	split := container.NewVSplit(editorPane, v.grid.build())
	split.Offset = 0.42
	v.view = split
	return v.view
}

// setCategory 按数据库类别裁剪查询页的功能：文档型显示集合与 filter/pipeline，
// 键值型只保留原生命令，关系型显示 SQL 与执行计划。
func (v *queryView) setCategory(cat dbCategory, caps domain.CapabilitySet) {
	previous := v.category
	v.category = cat
	v.caps = caps
	show := func(obj fyne.CanvasObject, visible bool) {
		if visible {
			obj.Show()
		} else {
			obj.Hide()
		}
	}
	show(v.collLabel, cat == categoryDocument)
	show(v.collection, cat == categoryDocument)
	if !caps.Has(domain.CapExplain) {
		v.explainBtn.Disable()
	} else if !v.busy() {
		v.explainBtn.Enable()
	}
	switch cat {
	case categoryKeyValue:
		v.dbLabel.SetText("逻辑库")
		v.editor.SetPlaceHolder("输入 Redis 命令（例如 SCAN 0 MATCH user:* COUNT 100）")
		v.dbEntry.SetPlaceHolder("逻辑库（如 db0），留空使用连接默认库")
		v.exportBtn.SetText("导出结果")
	case categoryDocument:
		v.dbLabel.SetText("数据库")
		v.editor.SetPlaceHolder("输入 MongoDB filter / pipeline JSON（例如 {\"status\":\"A\"}）")
		v.dbEntry.SetPlaceHolder("数据库（可选）")
		v.exportBtn.SetText("导出结果")
	default:
		v.dbLabel.SetText("数据库")
		v.editor.SetPlaceHolder("输入 SQL，可选中部分文本执行")
		v.dbEntry.SetPlaceHolder("当前数据库（可选）")
		v.exportBtn.SetText("导出 CSV")
	}
	if previous != cat && isDefaultQueryText(v.editor.Text) {
		v.editor.SetText(defaultQueryText(cat))
	}
}

func (v *queryView) busy() bool { return v.cancelBtn != nil && !v.cancelBtn.Disabled() }

func isDefaultQueryText(text string) bool {
	switch strings.TrimSpace(text) {
	case "SELECT VERSION();", "PING", "{}", "":
		return true
	default:
		return false
	}
}

func defaultQueryText(cat dbCategory) string {
	switch cat {
	case categoryKeyValue:
		return "PING"
	case categoryDocument:
		return "{}"
	default:
		return "SELECT VERSION();"
	}
}

func (v *queryView) applyCapabilities(conn string) {
	if conn == "" {
		return
	}
	v.caps = v.u.capsFor(conn)
	options := make([]string, 0, 3)
	if v.caps.Has(domain.CapSQL) {
		options = append(options, labelSQL)
	}
	if v.caps.Has(domain.CapMongoFilter) {
		options = append(options, labelMongoFilter, labelMongoPipeline)
	}
	if v.caps.Has(domain.CapKeyValues) {
		options = append(options, labelRedis)
	}
	if len(options) == 0 {
		options = []string{labelSQL}
	}
	v.language.Options = options
	selected := v.language.Selected
	if !stringIn(selected, options) {
		selected = options[0]
	}
	v.language.SetSelected(selected)
	if v.caps.Has(domain.CapExplain) {
		v.explainBtn.Enable()
	} else {
		v.explainBtn.Disable()
	}
	if v.caps.Has(domain.CapMongoFilter) {
		v.collection.Enable()
	} else {
		v.collection.Disable()
	}
	switch {
	case v.caps.Has(domain.CapKeyValues):
		v.editor.SetPlaceHolder("输入 Redis 命令（例如 SCAN 0 MATCH user:* COUNT 100）")
		if strings.TrimSpace(v.editor.Text) == "SELECT VERSION();" {
			v.editor.SetText("PING")
		}
	case !v.caps.Has(domain.CapSQL):
		v.editor.SetPlaceHolder("输入 MongoDB filter / pipeline JSON（例如 {\"status\":\"A\"}）")
		if strings.TrimSpace(v.editor.Text) == "SELECT VERSION();" {
			v.editor.SetText("{}")
		}
	default:
		v.editor.SetPlaceHolder("输入 SQL 或 MongoDB filter/pipeline JSON；可选中部分文本执行")
	}
	v.setCategory(categoryOf(v.caps), v.caps)
}

func (v *queryView) refreshConnections(list []domain.ConnectionConfig) {
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	previous := v.conn.Selected
	v.conn.Options = names
	if previous != "" {
		v.conn.SetSelected(previous)
	} else if len(names) > 0 {
		v.conn.SetSelected(names[0])
	}
	v.conn.Refresh()
}

func (v *queryView) setConnection(name string) { v.conn.SetSelected(name) }

func (v *queryView) setDatabase(database string) { v.dbEntry.SetText(database) }

func (v *queryView) currentLanguage() domain.QueryLanguage {
	switch v.language.Selected {
	case labelMongoFilter:
		return domain.LangMongoFilter
	case labelMongoPipeline:
		return domain.LangMongoPipeline
	case labelRedis:
		return domain.LangRedis
	default:
		return domain.LangSQL
	}
}

func (v *queryView) currentText() string {
	if v.currentLanguage() != domain.LangSQL {
		return strings.TrimSpace(v.editor.Text)
	}
	return currentStatement(v.editor)
}

func (v *queryView) setBusy(busy bool) {
	if busy {
		v.runBtn.Disable()
		v.explainBtn.Disable()
		v.cancelBtn.Enable()
	} else {
		v.runBtn.Enable()
		if v.caps == nil || v.caps.Has(domain.CapExplain) {
			v.explainBtn.Enable()
		}
		v.cancelBtn.Disable()
	}
}

func (v *queryView) cancelQuery() {
	if v.cancel != nil {
		v.cancel()
		v.u.setStatus("已请求取消查询")
	}
}

func currentStatement(e *widget.Entry) string {
	if selected := strings.TrimSpace(e.SelectedText()); selected != "" {
		return selected
	}
	text := e.Text
	lines := strings.Split(text, "\n")
	pos := 0
	for i := 0; i < e.CursorRow && i < len(lines); i++ {
		pos += len([]rune(lines[i])) + 1
	}
	pos += e.CursorColumn
	if stmt := strings.TrimSpace(core.StatementAt(text, pos)); stmt != "" {
		return stmt
	}
	if all := core.SplitStatements(text); len(all) > 0 {
		return all[len(all)-1]
	}
	return ""
}

var sqlClausePattern = regexp.MustCompile(
	`(?i)\s+(FROM|WHERE|GROUP\s+BY|HAVING|ORDER\s+BY|LIMIT|UNION\s+ALL|UNION|LEFT\s+JOIN|INNER\s+JOIN|JOIN)\s+`)

// formatSQL 只做可读的关键字换行，不改变语句语义；非 SQL 文本原样返回。
func formatSQL(text string) string {
	trimmed := strings.TrimSpace(text)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") &&
		!strings.HasPrefix(upper, "WITH") &&
		!strings.HasPrefix(upper, "SHOW") {
		return text
	}
	return sqlClausePattern.ReplaceAllStringFunc(trimmed, func(match string) string {
		keyword := strings.ToUpper(strings.Join(strings.Fields(match), " "))
		return "\n" + keyword + "\n  "
	})
}

func lineNumberGutter(text string) string {
	lines := strings.Count(text, "\n") + 1
	if lines > 2000 {
		lines = 2000
	}
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (v *queryView) run(explain bool) {
	text := v.currentText()
	if text == "" {
		v.u.showInfo("提示", "请输入或选中要执行的查询")
		return
	}
	conn := v.conn.Selected
	if conn == "" {
		v.u.showInfo("提示", "请先选择连接")
		return
	}
	db := v.dbEntry.Text
	lang := v.currentLanguage()
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.setBusy(true)
	v.grid.reset()
	v.u.setStatus("正在执行查询 …")
	sink := &gridSink{grid: v.grid}

	v.u.async(func() {
		defer fyne.Do(func() { v.setBusy(false) })
		q := domain.Query{
			Language: lang, Text: text, Database: db,
			Collection: strings.TrimSpace(v.collection.Text),
		}
		if explain {
			if _, err := v.u.core.Explain(ctx, conn, q, sink); err != nil {
				v.reportError(err)
			}
			return
		}
		if lang == domain.LangSQL && core.CheckReadOnly(text) != nil {
			res, err := v.u.core.ExecStatement(ctx, conn, text, db)
			if err != nil {
				v.reportError(err)
				return
			}
			fyne.Do(func() { v.grid.status.SetText(res.Message) })
			v.u.setStatus("%s", res.Message)
			return
		}
		if _, err := v.u.core.Execute(ctx, conn, q, sink); err != nil {
			v.reportError(err)
		}
	})
}

func (v *queryView) reportError(err error) {
	msg := domain.UserMessage(err)
	v.u.setStatus("执行失败: %s", msg)
	fyne.Do(func() { v.grid.status.SetText("执行失败: " + msg) })
	v.u.showError(err)
}

func exportPage(u *ui, page domain.ResultPage) {
	if len(page.Columns) == 0 {
		u.showInfo("提示", "当前没有可导出的结果")
		return
	}
	path := widget.NewEntry()
	home, _ := os.UserHomeDir()
	path.SetText(filepath.Join(home, "Downloads", "db-fly_result.csv"))
	dialog.ShowForm("导出 CSV", "导出", "取消", []*widget.FormItem{
		widget.NewFormItem("文件路径", path),
	}, func(ok bool) {
		if !ok {
			return
		}
		f, err := os.Create(path.Text)
		if err != nil {
			u.showError(err)
			return
		}
		defer f.Close()
		if err := export.CSV(f, page); err != nil {
			u.showError(err)
			return
		}
		u.showInfo("完成", "已导出到 "+path.Text)
	}, u.win)
}
