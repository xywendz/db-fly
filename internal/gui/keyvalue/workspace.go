//go:build windows && cgo

// Package keyvalue 实现键值型数据库的 GUI 表现层：键浏览 / 键详情 / 原生命令终端，
// 不使用关系型的字段网格与结构化过滤构建器。布局与功能与关系型、文档型完全隔离，
// 只通过 gui.Workspace 接口接入主壳。
package keyvalue

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/gui"
)

// Workspace 是键值型数据库的 workspace 实现。
type Workspace struct{ gui.BaseWorkspace }

func init() {
	gui.RegisterWorkspace(gui.CategoryKeyValue, Workspace{gui.NewBaseWorkspace(gui.CategoryKeyValue)})
}

func (w Workspace) QueryTabTitle() string { return "命令终端" }

// TableParts 键值型没有字段网格 / 外键等结构元数据，表页签只放键浏览。
func (w Workspace) TableParts(u *gui.UI, n gui.TreeNode) gui.TableTabParts {
	caps := u.CapsFor(n.ConnName())
	data := gui.NewDataView(u)
	content := data.BuildView()
	data.SetCategoryFor(gui.CategoryKeyValue, caps)
	return gui.NewTableTabParts(
		data,
		[]*container.TabItem{container.NewTabItem("键浏览", content)},
	)
}

func (w Workspace) ObjectMenu(u *gui.UI, n gui.TreeNode, _ domain.CapabilitySet) []*fyne.MenuItem {
	return []*fyne.MenuItem{
		fyne.NewMenuItem("打开键值", func() { u.OpenObjectData(n) }),
		fyne.NewMenuItem("查看键详情", func() { u.OpenKeyDetail(n) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("新建键", u.ShowCreateKey),
		fyne.NewMenuItem("删除键", func() { u.DeleteKey(n) }),
	}
}

func (w Workspace) DatabaseMenu(u *gui.UI, n gui.TreeNode, caps domain.CapabilitySet) []*fyne.MenuItem {
	items := w.BaseWorkspace.DatabaseMenu(u, n, caps)
	browse := fyne.NewMenuItem("浏览键", func() {
		u.OpenKeyBrowser(n.ConnName(), n.Database())
	})
	return append(items[:1], append([]*fyne.MenuItem{browse}, items[1:]...)...)
}

func (w Workspace) OnOpen(u *gui.UI, n gui.TreeNode) {
	u.SelectTab(gui.TabKeyDetail)
	u.SetStatus("已打开键 %s", n.Table())
}

// OpenData 键值型没有「表结构」概念：未选中具体键时打开键浏览，否则打开键详情。
func (w Workspace) OpenData(u *gui.UI, n gui.TreeNode) {
	if n.Table() == "" {
		u.OpenKeyBrowser(n.ConnName(), n.Database())
		return
	}
	u.OpenKeyDetail(n)
}

// OpenStructure 键值型不存在字段级结构，直接展示键详情。
func (w Workspace) OpenStructure(u *gui.UI, n gui.TreeNode) {
	u.OpenKeyDetail(n)
}

// CreateObject 键值型「新建键」保持轻量表单，不占用表结构样式的 tab。
func (w Workspace) CreateObject(u *gui.UI, conn, database string) {
	u.SetCurrentTarget(conn, database)
	u.ShowCreateKey()
}
