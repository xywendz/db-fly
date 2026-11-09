//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	core "github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/export"
)

const (
	labelSQL            = "SQL"
	labelMongoFilter    = "Mongo 过滤"
	labelMongoPipeline  = "Mongo 聚合"
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
	u         *ui
	mu        sync.RWMutex
	pages     []domain.ResultPage
	tabs      *container.AppTabs
	tableTabs []*container.TabItem
	tables    []*widget.Table
	active    int
	status    *widget.Label

	selected     widget.TableCellID
	hasSelection bool
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
			return len(page.Rows) + 1, len(page.Columns)
		},
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(g.cell(index, id))
		},
	)
	table.OnSelected = func(id widget.TableCellID) {
		g.mu.Lock()
		g.selected = id
		g.hasSelection = true
		g.mu.Unlock()
	}
	return table
}

func (g *resultGrid) build() fyne.CanvasObject {
	return container.NewBorder(nil, g.status, nil, nil, g.tabs)
}

func (g *resultGrid) cell(index int, id widget.TableCellID) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if index >= len(g.pages) {
		return ""
	}
	page := g.pages[index]
	if id.Row == 0 {
		if id.Col < len(page.Columns) {
			return page.Columns[id.Col].Name
		}
		return ""
	}
	row := id.Row - 1
	if row >= len(page.Rows) || id.Col >= len(page.Rows[row]) {
		return ""
	}
	return page.Rows[row][id.Col].Display()
}

func (g *resultGrid) reset() {
	g.mu.Lock()
	g.pages = nil
	g.hasSelection = false
	g.mu.Unlock()
	g.active = 0
	g.tableTabs = nil
	g.tables = nil
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
		g.tables[index].Refresh()
	}
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

	editor     *widget.Entry
	conn       *widget.Select
	dbEntry    *widget.Entry
	collection *widget.Entry
	language   *widget.Select
	grid       *resultGrid
	runBtn     *widget.Button
	explainBtn *widget.Button
	cancelBtn  *widget.Button
	exportBtn  *widget.Button
	favBtn     *widget.Button
	libBtn     *widget.Button
	cancel     context.CancelFunc
	caps       domain.CapabilitySet
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

	v.runBtn = widget.NewButton("执行", func() { v.run(false) })
	v.explainBtn = widget.NewButton("执行计划", func() { v.run(true) })
	v.cancelBtn = widget.NewButton("取消", v.cancelQuery)
	v.cancelBtn.Disable()
	v.exportBtn = widget.NewButton("导出 CSV", func() { exportPage(v.u, v.grid.currentPage()) })
	v.favBtn = widget.NewButton("保存收藏", func() {
		showSaveFavorite(v.u, v.conn.Selected, v.dbEntry.Text, v.collection.Text,
			string(v.currentLanguage()), v.currentText())
	})
	v.libBtn = widget.NewButton("历史 / 收藏", func() {
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

	toolbar := container.NewVBox(
		container.NewHBox(widget.NewLabel("连接"), v.conn, widget.NewLabel("数据库"), v.dbEntry,
			widget.NewLabel("语言"), v.language, widget.NewLabel("集合"), v.collection),
		container.NewHBox(v.runBtn, v.explainBtn, v.cancelBtn, v.exportBtn, v.favBtn, v.libBtn),
	)
	editorPane := container.NewBorder(toolbar, nil, nil, nil, v.editor)
	split := container.NewVSplit(editorPane, v.grid.build())
	split.Offset = 0.42
	return split
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
	if !v.caps.Has(domain.CapSQL) {
		v.editor.SetPlaceHolder("输入 MongoDB filter / pipeline JSON（例如 {\"status\":\"A\"}）")
		if strings.TrimSpace(v.editor.Text) == "SELECT VERSION();" {
			v.editor.SetText("{}")
		}
	} else {
		v.editor.SetPlaceHolder("输入 SQL 或 MongoDB filter/pipeline JSON；可选中部分文本执行")
	}
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

type dataView struct {
	u         *ui
	grid      *resultGrid
	info      *widget.Label
	editBtn   *widget.Button
	importBtn *widget.Button
	conn      string
	database  string
	table     string
}

func newDataView(u *ui) *dataView { return &dataView{u: u} }

func (v *dataView) build() fyne.CanvasObject {
	v.grid = newResultGrid(v.u)
	v.info = widget.NewLabel("请在左侧选择一张表")
	v.editBtn = widget.NewButton("编辑选中行", func() {
		showRowEditor(v.u, v.conn, v.database, v.table, v.grid, v.reload)
	})
	v.importBtn = widget.NewButton("导入文件", func() {
		showImportDialog(v.u, v.conn, v.database, v.table, v.reload)
	})
	toolbar := container.NewHBox(
		widget.NewButton("刷新数据", v.reload),
		v.editBtn,
		v.importBtn,
		widget.NewButton("导出 CSV", func() { exportPage(v.u, v.grid.currentPage()) }),
	)
	return container.NewBorder(toolbar, v.info, nil, nil, v.grid.build())
}

func (v *dataView) load(conn, database, table string) {
	v.conn, v.database, v.table = conn, database, table
	v.reload()
}

// reload 使用 Query.Ref 让各数据库模块自行生成预览语句，GUI 不拼接任何方言。
func (v *dataView) reload() {
	if v.table == "" || v.conn == "" {
		v.u.showInfo("提示", "请先在左侧选择一张表")
		return
	}
	q := domain.Query{
		Ref:      domain.ObjectRef{Database: v.database, Name: v.table},
		Database: v.database, MaxRows: defaultPreviewLimit,
	}
	v.grid.reset()
	v.info.SetText(fmt.Sprintf("%s · 最多 %d 行", v.database+"."+v.table, defaultPreviewLimit))
	sink := &gridSink{grid: v.grid}
	v.u.async(func() {
		out, err := v.u.core.Execute(context.Background(), v.conn, q, sink)
		if err != nil {
			v.u.showError(err)
			return
		}
		fyne.Do(func() {
			v.info.SetText(fmt.Sprintf("%s · 最多 %d 行 · %d ms",
				v.database+"."+v.table, defaultPreviewLimit, out.Summary.DurationMs))
		})
	})
}

func exportPage(u *ui, page domain.ResultPage) {
	if len(page.Columns) == 0 {
		u.showInfo("提示", "当前没有可导出的结果")
		return
	}
	path := widget.NewEntry()
	home, _ := os.UserHomeDir()
	path.SetText(filepath.Join(home, "Downloads", "dbclient_result.csv"))
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
