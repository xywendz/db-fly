//go:build windows && cgo

// Package document 实现文档型数据库的 GUI 表现层：集合数据 tab、Filter / 聚合查询 tab、
// 集合结构子页签，以及查看集合定义 / 修改校验器等对象级动作。布局与功能与关系型、
// 键值型完全隔离，只通过 gui.Workspace 接口接入主壳。
package document

import (
	"fyne.io/fyne/v2"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/gui"
)

// Workspace 是文档型数据库的 workspace 实现。
type Workspace struct{ gui.BaseWorkspace }

func init() {
	gui.RegisterWorkspace(gui.CategoryDocument, Workspace{gui.NewBaseWorkspace(gui.CategoryDocument)})
}

func (w Workspace) QueryTabTitle() string { return "Filter / 聚合" }

func (w Workspace) ObjectMenu(u *gui.UI, n gui.TreeNode, _ domain.CapabilitySet) []*fyne.MenuItem {
	return []*fyne.MenuItem{
		fyne.NewMenuItem("打开集合数据", func() { u.OpenObjectData(n) }),
		fyne.NewMenuItem("重命名集合", func() { u.RenameObject(n) }),
		fyne.NewMenuItem("导出数据（向导）", func() { u.OpenExportWizardForNode(n) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("查看集合定义", func() { u.ShowObjectDefinition(n) }),
		fyne.NewMenuItem("修改集合校验器", func() { u.ModifyCollectionValidator(n) }),
		fyne.NewMenuItem("删除集合", func() { u.DeleteObject(n) }),
	}
}

func (w Workspace) OnOpen(u *gui.UI, n gui.TreeNode) {
	u.SelectTab(gui.TabData)
	u.SetStatus("已打开集合 %s", n.Table())
}
