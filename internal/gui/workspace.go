//go:build windows && cgo

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/db-fly/db-fly/internal/domain"
)

// tableTabParts 是一个表页签内部的布局与视图实例，由各类别 workspace 自行装配。
type tableTabParts struct {
	subTabs []*container.TabItem
	data    *dataView
	schema  *schemaView
	keyView *keyDetailView
}

// workspace 是「按数据库类别隔离表现层」的扩展点，类似 provider.Provider：
// 关系型 / 文档型 / 键值型各自实现自己的页签布局、右键菜单与树交互，
// gui 主壳只按 CapabilitySet 推导的类别做分派，不写 if-kind 分支。
type workspace interface {
	Category() dbCategory
	QueryTabTitle() string
	TableParts(u *ui, n treeNode) tableTabParts
	ObjectMenu(u *ui, n treeNode, caps domain.CapabilitySet) []*fyne.MenuItem
	DatabaseMenu(u *ui, n treeNode, caps domain.CapabilitySet) []*fyne.MenuItem
	ConnectionMenu(u *ui, n treeNode, caps domain.CapabilitySet) []*fyne.MenuItem
	OnSelect(u *ui, n treeNode)
	OnOpen(u *ui, n treeNode)
	CreateObject(u *ui, conn, database string)
	// OpenData / OpenStructure 把「打开数据 / 结构」这类随类别变化的动作收敛到 workspace，
	// 使主壳 selectTab 不再按 category 写分支，保证各类别布局与功能隔离。
	OpenData(u *ui, n treeNode)
	OpenStructure(u *ui, n treeNode)
}

// workspaces 由各数据库类别的子包在 init() 中通过 RegisterWorkspace 填充，
// 主壳只按 CapabilitySet 推导类别并分派，不持有任何具体类别类型。
var workspaces = map[dbCategory]workspace{}

func workspaceFor(cat dbCategory) workspace {
	if w, ok := workspaces[cat]; ok {
		return w
	}
	return workspaces[categoryRelational]
}

// baseWorkspace 提供三类数据库共有的连接 / 库级菜单与树单击行为。
type baseWorkspace struct{ cat dbCategory }

func (w baseWorkspace) Category() dbCategory { return w.cat }

func (w baseWorkspace) ConnectionMenu(u *ui, n treeNode, caps domain.CapabilitySet) []*fyne.MenuItem {
	return []*fyne.MenuItem{
		fyne.NewMenuItem("展开 / 刷新下级", func() {
			u.refreshChildren(connUID(n.connID))
		}),
		fyne.NewMenuItem("刷新连接树", func() { u.refreshConnections() }),
		fyne.NewMenuItemSeparator(),
		menuItem("新建数据库", caps.Has(domain.CapCreateDatabase), func() {
			u.showCreateDatabase(n.connName)
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("连接管理", u.showConnections),
	}
}

func (w baseWorkspace) DatabaseMenu(u *ui, n treeNode, caps domain.CapabilitySet) []*fyne.MenuItem {
	cat := w.cat
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("展开 / 刷新"+cat.objectName()+"列表", func() {
			u.refreshChildren(dbUID(n.connID, n.database))
		}),
	}
	if caps.Has(domain.CapCreateTable) {
		items = append(items, fyne.NewMenuItem("新建"+cat.objectName(), func() {
			u.showCreateObjectFor(n.connName, n.database)
		}))
	}
	return append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("导出数据库（向导）", func() {
			u.openExportWizardForNode(n)
		}),
		fyne.NewMenuItemSeparator(),
		menuItem("新建数据库", caps.Has(domain.CapCreateDatabase), func() {
			u.showCreateDatabase(n.connName)
		}),
		menuItem("修改数据库", caps.Has(domain.CapAlterDatabase), func() {
			u.showAlterDatabase(n.connName, n.database)
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("连接管理", u.showConnections),
	)
}

// OnSelect 单击只选中节点、同步面包屑，不切换或加载任何视图；
// 具体数据由双击打开的独立页签承载。
func (w baseWorkspace) OnSelect(u *ui, n treeNode) {
	switch n.kind {
	case nodeConn:
		u.tree.OpenBranch(n.uid)
		u.setStatus("已选择连接 %s", u.categoryStatus(n.connName))
	case nodeDatabase:
		u.tree.OpenBranch(n.uid)
		u.setStatus("已选择数据库 %s · %s", n.database, w.cat.name())
	default:
		u.setStatus("已选中%s %s（双击打开新标签页，右键查看更多功能）",
			w.cat.objectName(), n.table)
	}
}

func (w baseWorkspace) OnOpen(u *ui, n treeNode) {
	u.selectTab(tabData)
	u.setStatus("已打开%s %s", w.cat.objectName(), n.table)
}

func (w baseWorkspace) CreateObject(u *ui, conn, database string) {
	if u.tabs == nil {
		return
	}
	u.tabs.openCreateTable(conn, database, w.cat)
}

// OpenData 关系型 / 文档型默认打开「数据」子页签。
func (w baseWorkspace) OpenData(u *ui, n treeNode) {
	u.tabs.openTable(n)
}

// OpenStructure 打开表页签并切到结构子页签（关系型=字段/索引，文档型=集合结构）。
func (w baseWorkspace) OpenStructure(u *ui, n treeNode) {
	e := u.tabs.openTable(n)
	if e != nil && e.subTabs != nil {
		e.subTabs.SelectIndex(1)
	}
}

func (w baseWorkspace) QueryTabTitle() string { return "SQL 查询" }

// TableParts 是关系型 / 文档型共用的表页签布局：数据 + 结构两个子页签。
func (w baseWorkspace) TableParts(u *ui, n treeNode) tableTabParts {
	caps := u.capsFor(n.connName)
	data := newDataView(u)
	dataContent := data.build()
	data.setCategory(w.cat, caps)
	schema := newSchemaView(u)
	schemaContent := schema.build()
	schema.setCategory(w.cat, caps)
	return tableTabParts{
		data: data, schema: schema,
		subTabs: []*container.TabItem{
			container.NewTabItem("数据", dataContent),
			container.NewTabItem(w.cat.objectName()+"结构", schemaContent),
		},
	}
}
