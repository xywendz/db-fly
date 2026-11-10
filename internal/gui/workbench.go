//go:build windows && cgo

package gui

import (
	"fmt"

	"github.com/db-fly/db-fly/internal/domain"
)

// dbCategory 由模块的 CapabilitySet 推导，主壳不按 Kind 分支，
// 只把类别交给 workspace 注册表装配对应布局与功能。
type dbCategory int

const (
	categoryRelational dbCategory = iota
	categoryDocument
	categoryKeyValue
)

func categoryOf(caps domain.CapabilitySet) dbCategory {
	switch {
	case caps.Has(domain.CapKeyValues):
		return categoryKeyValue
	case caps.Has(domain.CapMongoFilter) || caps.Has(domain.CapJSONTree):
		return categoryDocument
	default:
		return categoryRelational
	}
}

func (c dbCategory) name() string {
	switch c {
	case categoryDocument:
		return "文档型数据库"
	case categoryKeyValue:
		return "键值型数据库"
	default:
		return "关系型数据库"
	}
}

func (c dbCategory) objectName() string {
	switch c {
	case categoryDocument:
		return "集合"
	case categoryKeyValue:
		return "键"
	default:
		return "表"
	}
}

// workspaceTab 是逻辑页签，供右键菜单 / 树交互表达「打开哪种视图」。
type workspaceTab int

const (
	tabQuery workspaceTab = iota
	tabSchema
	tabData
	tabKeyDetail
)

// selectTab 把逻辑页签映射到多文档容器：同对象只激活已有 tab，不重复创建。
// 具体打开哪种视图由当前类别的 workspace 决定，主壳不写 if-kind 分支。
func (u *ui) selectTab(tab workspaceTab) {
	if u.tabs == nil {
		return
	}
	switch tab {
	case tabQuery:
		u.tabs.openQuery(u.currentConn, u.currentDB)
	case tabData:
		if u.currentTable == "" {
			return
		}
		workspaceFor(u.category).OpenData(u, u.currentNode())
	case tabSchema:
		if u.currentTable == "" {
			return
		}
		workspaceFor(u.category).OpenStructure(u, u.currentNode())
	case tabKeyDetail:
		if u.currentTable != "" {
			u.tabs.openKeyDetail(u.currentNode())
		}
	}
}

// applyWorkspace 按当前连接的能力更新类别与视图配置。
// 多文档模式下不再替换整个页签集合：不同连接的 tab 可以并存。
func (u *ui) applyWorkspace(conn string) {
	if conn == "" {
		if list, err := u.core.ListConnections(); err == nil && len(list) > 0 {
			conn = list[0].Name
		}
	}
	caps := u.capsFor(conn)
	cat := categoryOf(caps)
	if u.workspaceReady && u.workspaceConn == conn && cat == u.category {
		return
	}
	u.category = cat
	u.workspaceConn = conn
	u.workspaceReady = true
	u.categoryBar.SetText(cat.name())
	u.updateBreadcrumb()

	// 动作宿主视图与应用能力同步。
	u.query.setCategory(cat, caps)
	if conn != "" {
		u.query.applyCapabilities(conn)
	}
	u.data.setCategory(cat, caps)
	u.schema.setCategory(cat, caps)
	u.keyView.setCategory(cat, caps)
	// 当前激活的查询 tab 也要跟随连接切换语言与占位提示。
	if q := u.tabs.activeQuery(); q != nil {
		q.setCategory(cat, caps)
		if conn != "" {
			q.applyCapabilities(conn)
		}
	}
}

// showCreateObjectFor 由数据库节点右键调用，直接指定目标连接与库。
func (u *ui) showCreateObjectFor(conn, database string) {
	if conn == "" || database == "" {
		u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	u.currentConn, u.currentDB = conn, database
	u.updateBreadcrumb()
	u.showCreateObject()
}

// showCreateObject 分派给当前类别的 workspace 实现（关系型/文档型打开新 tab）。
func (u *ui) showCreateObject() {
	if !u.requireConnection() {
		return
	}
	if u.currentDB == "" {
		u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	workspaceFor(u.category).CreateObject(u, u.currentConn, u.currentDB)
}

// categoryForConn 供树节点标签等处使用，不建立真实连接。
func (u *ui) categoryForConn(conn string) dbCategory {
	return categoryOf(u.capsFor(conn))
}

func (u *ui) categoryStatus(conn string) string {
	return fmt.Sprintf("%s · %s", conn, u.category.name())
}
