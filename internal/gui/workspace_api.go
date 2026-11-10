//go:build windows && cgo

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/db-fly/db-fly/internal/domain"
)

// 以下别名把 gui 内部类型暴露给按数据库类型拆分出来的子包
// （internal/gui/relational、document、keyvalue），使各子包无需改动内部
// 类型名即可实现 gui.Workspace，并把「布局与功能」严格按类别隔离。
type (
	UI            = ui
	TreeNode      = treeNode
	DataView      = dataView
	SchemaView    = schemaView
	QueryView     = queryView
	KeyDetailView = keyDetailView
	TableDesigner = tableDesigner
	TableTabParts = tableTabParts
	BaseWorkspace = baseWorkspace
	Category      = dbCategory
	Workspace     = workspace
	TabKind       = workspaceTab
	NodeKind      = nodeKind
)

const (
	CategoryRelational = categoryRelational
	CategoryDocument   = categoryDocument
	CategoryKeyValue   = categoryKeyValue

	TabQuery     = tabQuery
	TabData      = tabData
	TabSchema    = tabSchema
	TabKeyDetail = tabKeyDetail

	NodeConn     = nodeConn
	NodeDatabase = nodeDatabase
	NodeObject   = nodeObject
)

// Name / ObjectName 暴露类别的中文名，供子包构建菜单 / 状态文案。
func (c Category) Name() string       { return c.name() }
func (c Category) ObjectName() string { return c.objectName() }

// MenuItem 是内部 menuItem 构造函数的对外包装（menuItem 本身是函数而非类型）。
func MenuItem(label string, enabled bool, action func()) *fyne.MenuItem {
	return menuItem(label, enabled, action)
}

// NewBaseWorkspace 构造带类别的基础 workspace（子包用来内嵌）。
func NewBaseWorkspace(cat Category) BaseWorkspace {
	return BaseWorkspace{cat: cat}
}

// RegisterWorkspace 由各类别子包的 init() 调用，把自身注册进 gui 的类别表，
// 从而 gui 主壳只按 CapabilitySet 推导类别并分派，不依赖具体子包类型。
func RegisterWorkspace(cat Category, w Workspace) {
	workspaces[cat] = w
}

// newNodeKind / newTreeNode 辅助构造（子包一般直接复用 gui 节点的字段访问器）。

// treeNode 字段访问器：子包通过它们读取节点信息，避免导出内部字段。
func (n treeNode) ConnName() string { return n.connName }
func (n treeNode) Database() string { return n.database }
func (n treeNode) Table() string    { return n.table }
func (n treeNode) Kind() NodeKind   { return n.kind }
func (n treeNode) Label() string    { return n.label }
func (n treeNode) UID() string      { return n.uid }
func (n treeNode) ConnID() string   { return n.connID }

// 以下方法把 ui 内部动作暴露给子包，主壳与各子包都通过这些稳定入口交互。
func (u *ui) OpenObjectData(n TreeNode)                    { u.openObjectData(n) }
func (u *ui) RenameObject(n TreeNode)                      { u.renameObject(n) }
func (u *ui) OpenExportWizardForNode(n TreeNode)           { u.openExportWizardForNode(n) }
func (u *ui) TableAction(n TreeNode, a domain.TableAction) { u.tableAction(n, a) }
func (u *ui) AlterObjectStructure(n TreeNode)              { u.alterObjectStructure(n) }
func (u *ui) DeleteObject(n TreeNode)                      { u.deleteObject(n) }
func (u *ui) ShowDatabaseStructure(conn, db string)        { u.showDatabaseStructure(conn, db) }
func (u *ui) SetStatus(format string, args ...any)         { u.setStatus(format, args...) }
func (u *ui) ShowObjectDefinition(n TreeNode)              { u.showObjectDefinition(n) }
func (u *ui) ModifyCollectionValidator(n TreeNode)         { u.modifyCollectionValidator(n) }
func (u *ui) OpenKeyDetail(n TreeNode)                     { u.openKeyDetail(n) }
func (u *ui) ShowCreateKey()                               { u.showCreateKey() }
func (u *ui) DeleteKey(n TreeNode)                         { u.deleteKey(n) }
func (u *ui) OpenKeyBrowser(conn, db string)               { u.openKeyBrowser(conn, db) }
func (u *ui) CapsFor(conn string) domain.CapabilitySet     { return u.capsFor(conn) }
func (u *ui) SelectTab(t TabKind)                          { u.selectTab(t) }
func (u *ui) CurrentConn() string                          { return u.currentConn }
func (u *ui) CurrentDB() string                            { return u.currentDB }
func (u *ui) SetCurrentTarget(conn, database string)       { u.currentConn, u.currentDB = conn, database }

// 视图构造与生命周期方法（子包用它们装配表页签）。
func NewDataView(u *UI) *DataView     { return newDataView(u) }
func NewSchemaView(u *UI) *SchemaView { return newSchemaView(u) }

// NewTableTabParts 装配一个表页签（子包返回时用），data 为默认显示的视图，
// subTabs 为可切换的子页签（关系型/文档型一般为「数据 + 结构」）。
func NewTableTabParts(data *DataView, subTabs []*container.TabItem) TableTabParts {
	return TableTabParts{data: data, subTabs: subTabs}
}

func (v *DataView) BuildView() fyne.CanvasObject { return v.build() }
func (v *DataView) SetCategoryFor(cat Category, caps domain.CapabilitySet) {
	v.setCategory(cat, caps)
}
func (v *SchemaView) BuildView() fyne.CanvasObject { return v.build() }
func (v *SchemaView) SetCategoryFor(cat Category, caps domain.CapabilitySet) {
	v.setCategory(cat, caps)
}
