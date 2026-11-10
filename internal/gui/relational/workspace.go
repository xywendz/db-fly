//go:build windows && cgo

// Package relational 实现关系型数据库的 GUI 表现层：表 tab（数据 + 结构子页签）、
// SQL 查询 tab，以及改表名 / 导出 / TRUNCATE / 改结构 / 删表等对象级动作。
// 这些布局与功能与文档型、键值型完全隔离，只通过 gui.Workspace 接口接入主壳。
package relational

import (
	"fyne.io/fyne/v2"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/gui"
)

// Workspace 是关系型数据库的 workspace 实现。
type Workspace struct{ gui.BaseWorkspace }

func init() {
	gui.RegisterWorkspace(gui.CategoryRelational, Workspace{gui.NewBaseWorkspace(gui.CategoryRelational)})
}

func (w Workspace) QueryTabTitle() string { return "SQL 查询" }

func (w Workspace) ObjectMenu(u *gui.UI, n gui.TreeNode, caps domain.CapabilitySet) []*fyne.MenuItem {
	return []*fyne.MenuItem{
		fyne.NewMenuItem("打开表数据", func() { u.OpenObjectData(n) }),
		fyne.NewMenuItem("修改表名", func() { u.RenameObject(n) }),
		fyne.NewMenuItem("导出数据（向导）", func() { u.OpenExportWizardForNode(n) }),
		fyne.NewMenuItemSeparator(),
		gui.MenuItem("TRUNCATE", caps.Has(domain.CapTruncateTable), func() {
			u.TableAction(n, domain.TableActionTruncate)
		}),
		gui.MenuItem("清空数据 (DELETE)", caps.Has(domain.CapEmptyTable), func() {
			u.TableAction(n, domain.TableActionEmpty)
		}),
		fyne.NewMenuItemSeparator(),
		gui.MenuItem("修改表结构", caps.Has(domain.CapAlterTable), func() {
			u.AlterObjectStructure(n)
		}),
		gui.MenuItem("删除表", caps.Has(domain.CapDropObject), func() { u.DeleteObject(n) }),
	}
}

func (w Workspace) DatabaseMenu(u *gui.UI, n gui.TreeNode, caps domain.CapabilitySet) []*fyne.MenuItem {
	items := w.BaseWorkspace.DatabaseMenu(u, n, caps)
	// 在「连接管理」前插入关系型专属的库结构快照入口。
	extra := fyne.NewMenuItem("查看数据库结构", func() {
		u.ShowDatabaseStructure(n.ConnName(), n.Database())
	})
	return append(items[:len(items)-2], append([]*fyne.MenuItem{extra}, items[len(items)-2:]...)...)
}
