//go:build windows && cgo

package gui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/gui/components"
)

// 多文档工作区：每张表、每个查询、每个新建表设计器各占一个可关闭页签。
type editorTabKind int

const (
	tabKindQuery editorTabKind = iota
	tabKindTable
	tabKindKey
	tabKeyBrowser
	tabKindCreate
	tabKindExport
)

type editorTab struct {
	id             string
	kind           editorTabKind
	conn, database string
	table          string
	item           *components.TabItem
	content        fyne.CanvasObject
	data           *dataView
	schema         *schemaView
	query          *queryView
	keyView        *keyDetailView
	designer       *tableDesigner
	subTabs        *container.AppTabs
}

type tabManager struct {
	u       *ui
	strip   *components.TabStrip
	entries map[string]*editorTab
	seq     int
}

func newTabManager(u *ui) *tabManager {
	return &tabManager{u: u, entries: make(map[string]*editorTab)}
}

func (m *tabManager) build() fyne.CanvasObject {
	m.strip = components.NewTabStrip()
	m.strip.SetOnChanged(func(item *components.TabItem) {
		if item == nil {
			return
		}
		if e := m.entries[item.ID]; e != nil {
			m.onActivated(e)
		}
	})
	view := m.strip.Build()
	m.openQuery("", "")
	return view
}

func (m *tabManager) onActivated(e *editorTab) {
	u := m.u
	if e.conn != "" {
		u.currentConn = e.conn
	}
	if e.database != "" {
		u.currentDB = e.database
	}
	u.currentTable = e.table
	u.updateBreadcrumb()
	u.applyWorkspace(u.currentConn)
}

func (m *tabManager) add(e *editorTab, title string, closable bool) {
	item := &components.TabItem{
		ID: e.id, Title: title, Content: e.content, Closable: closable,
		OnClose: func() { delete(m.entries, e.id) },
	}
	e.item = item
	m.entries[e.id] = e
	m.strip.Add(item)
}

func (m *tabManager) active() *editorTab {
	if m.strip == nil {
		return nil
	}
	item := m.strip.Active()
	if item == nil {
		return nil
	}
	return m.entries[item.ID]
}

func (m *tabManager) activeQuery() *queryView {
	if e := m.active(); e != nil {
		return e.query
	}
	return nil
}

// openQuery 新建一个查询页签；每个页签持有独立的 queryView 实例。
func (m *tabManager) openQuery(conn, database string) *editorTab {
	m.seq++
	id := fmt.Sprintf("query|%d", m.seq)
	q := newQueryView(m.u)
	content := q.build()
	if conn == "" {
		conn = m.u.currentConn
	}
	if database == "" {
		database = m.u.currentDB
	}
	caps := m.u.capsFor(conn)
	cat := categoryOf(caps)
	q.setCategory(cat, caps)
	if conn != "" {
		q.setConnection(conn)
		q.applyCapabilities(conn)
	}
	q.setDatabase(database)
	e := &editorTab{
		id: id, kind: tabKindQuery, conn: conn, database: database,
		query: q, content: content,
	}
	m.add(e, workspaceFor(cat).QueryTabTitle(), true)
	return e
}

// openTable 为一张表创建独立页签；同 conn+db+table 只开一个。
func (m *tabManager) openTable(n treeNode) *editorTab {
	id := fmt.Sprintf("table|%s|%s|%s", n.connName, n.database, n.table)
	if e := m.entries[id]; e != nil {
		m.strip.Select(id)
		return e
	}
	cat := categoryOf(m.u.capsFor(n.connName))
	// 表页签内部布局由该数据库类别的 workspace 决定。
	parts := workspaceFor(cat).TableParts(m.u, n)
	subTabs := container.NewAppTabs(parts.subTabs...)
	header := container.NewHBox(
		widget.NewLabel(n.table+" @ "+n.database),
		widget.NewSeparator(),
		widget.NewLabel(cat.name()),
		layout.NewSpacer(),
		widget.NewButton("刷新", func() {
			if parts.data != nil {
				parts.data.reload()
			}
			if parts.schema != nil {
				parts.schema.reload()
			}
		}),
		widget.NewButton("新建"+cat.objectName(), func() {
			m.openCreateTable(n.connName, n.database, cat)
		}),
	)
	content := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, subTabs,
	)
	e := &editorTab{
		id: id, kind: tabKindTable, conn: n.connName, database: n.database, table: n.table,
		content: content, data: parts.data, schema: parts.schema,
		keyView: parts.keyView, subTabs: subTabs,
	}
	m.add(e, n.table+" @ "+n.database, true)
	if parts.data != nil {
		parts.data.load(n.connName, n.database, n.table)
	}
	if parts.schema != nil {
		parts.schema.load(n.connName, n.database, n.table)
	}
	return e
}

func (m *tabManager) openKeyDetail(n treeNode) *editorTab {
	id := fmt.Sprintf("key|%s|%s|%s", n.connName, n.database, n.table)
	if e := m.entries[id]; e != nil {
		m.strip.Select(id)
		return e
	}
	caps := m.u.capsFor(n.connName)
	view := newKeyDetailView(m.u)
	content := view.build()
	view.setCategory(categoryKeyValue, caps)
	header := container.NewHBox(
		widget.NewLabel("键详情 · "+n.table), layout.NewSpacer(),
		widget.NewButton("刷新", view.reload),
	)
	wrapped := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, content,
	)
	e := &editorTab{
		id: id, kind: tabKindKey, conn: n.connName, database: n.database, table: n.table,
		content: wrapped, keyView: view,
	}
	m.add(e, n.table+" · 键详情", true)
	view.load(n.connName, n.database, n.table)
	return e
}

func (m *tabManager) openKeyBrowser(conn, database string) *editorTab {
	id := fmt.Sprintf("browser|%s|%s", conn, database)
	if e := m.entries[id]; e != nil {
		m.strip.Select(id)
		return e
	}
	caps := m.u.capsFor(conn)
	data := newDataView(m.u)
	content := data.build()
	data.setCategory(categoryKeyValue, caps)
	header := container.NewHBox(
		widget.NewLabel("键浏览 @ "+database), layout.NewSpacer(),
		widget.NewButton("刷新", data.reload),
	)
	wrapped := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, content,
	)
	e := &editorTab{
		id: id, kind: tabKeyBrowser, conn: conn, database: database,
		content: wrapped, data: data,
	}
	m.add(e, "键浏览 @ "+database, true)
	data.load(conn, database, "")
	return e
}

// openCreateTable 把表结构样式的新建对象设计器放进独立页签，不再弹窗。
func (m *tabManager) openCreateTable(conn, database string, cat dbCategory) *editorTab {
	if conn == "" || database == "" {
		m.u.showInfo("提示", "请先在左侧选择一个数据库")
		return nil
	}
	m.seq++
	id := fmt.Sprintf("create|%d", m.seq)
	designer := newTableDesigner(m.u, conn, database, cat)
	content := designer.Build()
	e := &editorTab{
		id: id, kind: tabKindCreate, conn: conn, database: database,
		content: content, designer: designer,
	}
	m.add(e, "新建"+cat.objectName()+" @ "+database, true)
	return e
}

// openExportWizard 打开按步骤执行的导出向导页签；同一连接/库/表只开一个。
func (m *tabManager) openExportWizard(conn, database, table string) *editorTab {
	id := "export|"
	if conn != "" {
		id += conn + "|" + database + "|" + table
	} else {
		m.seq++
		id += fmt.Sprintf("new|%d", m.seq)
	}
	if e := m.entries[id]; e != nil {
		m.strip.Select(id)
		return e
	}
	w := newExportWizard(m.u)
	w.conn, w.database, w.table = conn, database, table
	w.wholeDB = table == ""
	content := w.build()
	e := &editorTab{
		id: id, kind: tabKindExport, conn: conn, database: database, table: table,
		content: content,
	}
	m.add(e, "导出向导", true)
	return e
}

// refreshObject 在结构/数据脚本执行成功后刷新所有匹配的表页签。
func (m *tabManager) refreshObject(conn, database, table string) {
	for _, e := range m.entries {
		if e.kind != tabKindTable || e.conn != conn ||
			e.database != database || e.table != table {
			continue
		}
		if e.data != nil {
			e.data.reload()
		}
		if e.schema != nil {
			e.schema.reload()
		}
	}
}

func (m *tabManager) len() int {
	if m.strip == nil {
		return 0
	}
	return m.strip.Len()
}

// openNewQuery 供主工具栏「新建查询」调用。
func (u *ui) openNewQuery() {
	if u.tabs != nil {
		u.tabs.openQuery(u.currentConn, u.currentDB)
	}
}

func (u *ui) openKeyBrowser(conn, database string) {
	if u.tabs != nil {
		u.tabs.openKeyBrowser(conn, database)
	}
}

func (u *ui) openExportWizard(conn, database, table string) {
	if u.tabs != nil {
		u.tabs.openExportWizard(conn, database, table)
	}
}

// openExportWizardForNode 由右键菜单调用，按节点类型预填向导。
func (u *ui) openExportWizardForNode(n treeNode) {
	switch n.kind {
	case nodeDatabase:
		u.openExportWizard(n.connName, n.database, "")
	default:
		u.openExportWizard(n.connName, n.database, n.table)
	}
}

// currentNode 由面包屑状态构造一个轻量节点，供 selectTab 兼容入口使用。
func (u *ui) currentNode() treeNode {
	return treeNode{
		uid: objUID(u.currentConn, u.currentDB, u.currentTable), kind: nodeObject,
		connID: u.currentConn, connName: u.currentConn,
		database: u.currentDB, table: u.currentTable, label: u.currentTable,
	}
}
