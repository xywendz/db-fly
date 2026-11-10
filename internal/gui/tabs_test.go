//go:build windows && cgo

package gui

import (
	"testing"
)

// TestTabShowsTableName 验证双击/打开表后，页签标题确实是「表 @ 库」而不是空白。
func TestTabShowsTableName(t *testing.T) {
	u, _ := newTestUI(t)
	node := treeNode{
		uid: "obj|mysql|app|admins", kind: nodeObject,
		connID: "mysql", connName: "mysql", database: "app", table: "admins",
	}
	u.tabs.openTable(node)
	active := u.tabs.strip.Active()
	if active == nil {
		t.Fatal("打开表后应有一个激活页签")
	}
	if active.Title != "admins @ app" {
		t.Fatalf("表页签标题应为 %q，实际 %q", "admins @ app", active.Title)
	}

	// 文档型 / 键值型页签标题同样应带库与对象名。
	docNode := node
	docNode.connName, docNode.table, docNode.uid = "mongodb", "orders", "obj|mongodb|app|orders"
	u.tabs.openTable(docNode)
	if got := u.tabs.strip.Active().Title; got != "orders @ app" {
		t.Fatalf("文档页签标题应为 %q，实际 %q", "orders @ app", got)
	}
}
