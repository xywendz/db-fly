//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/dbclient/dbclient/internal/domain"
)

type nodeKind int

const (
	nodeConn nodeKind = iota
	nodeDatabase
	nodeObject
)

type treeNode struct {
	uid      string
	kind     nodeKind
	label    string
	connID   string
	connName string
	database string
	table    string
}

func connUID(id string) string { return "conn|" + id }

func dbUID(id, database string) string { return "db|" + id + "|" + database }

func objUID(id, database, table string) string {
	return "obj|" + id + "|" + database + "|" + table
}

func (u *ui) buildTree() *widget.Tree {
	t := widget.NewTree(
		func(uid widget.TreeNodeID) []widget.TreeNodeID {
			return u.childUIDs(string(uid))
		},
		func(uid widget.TreeNodeID) bool {
			if uid == "" {
				return true
			}
			n, ok := u.node(string(uid))
			return ok && n.kind != nodeObject
		},
		func(bool) fyne.CanvasObject { return widget.NewLabel("") },
		func(uid widget.TreeNodeID, _ bool, o fyne.CanvasObject) {
			label := o.(*widget.Label)
			if uid == "" {
				label.SetText("连接")
				return
			}
			if n, ok := u.node(string(uid)); ok {
				label.SetText(n.label)
			}
		},
	)
	// 展开分支时异步加载子节点，避免网络 IO 阻塞 UI 主线程。
	t.OnBranchOpened = func(uid widget.TreeNodeID) {
		id := string(uid)
		u.async(func() {
			u.loadChildren(id)
			fyne.Do(func() { t.Refresh() })
		})
	}
	t.OnSelected = func(uid widget.TreeNodeID) { u.onSelect(string(uid)) }
	return t
}

func (u *ui) loadChildren(uid string) {
	if uid == "" {
		return // 根连接列表由 refreshConnections 同步维护
	}
	n, ok := u.node(uid)
	if !ok || n.kind == nodeObject {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch n.kind {
	case nodeConn:
		databases, err := u.core.ListDatabases(ctx, n.connName)
		if err != nil {
			u.setStatus("加载数据库失败: %s", err.Error())
			return
		}
		nodes := make([]treeNode, 0, len(databases))
		for _, d := range databases {
			label := d.Name
			if d.System {
				label += " (系统)"
			}
			nodes = append(nodes, treeNode{
				uid: dbUID(n.connID, d.Name), kind: nodeDatabase, label: label,
				connID: n.connID, connName: n.connName, database: d.Name,
			})
		}
		u.setChildren(uid, nodes)
		u.setStatus("连接 %s 下有 %d 个数据库", n.connName, len(nodes))

	case nodeDatabase:
		objects, err := u.core.ListObjects(ctx, n.connName, n.database, "")
		if err != nil {
			u.setStatus("加载对象失败: %s", err.Error())
			return
		}
		nodes := make([]treeNode, 0, len(objects))
		for _, obj := range objects {
			label := obj.Name
			if obj.Type == domain.ObjectView {
				label += " (视图)"
			}
			nodes = append(nodes, treeNode{
				uid: objUID(n.connID, n.database, obj.Name), kind: nodeObject, label: label,
				connID: n.connID, connName: n.connName, database: n.database, table: obj.Name,
			})
		}
		u.setChildren(uid, nodes)
		u.setStatus("数据库 %s 下有 %d 个对象", n.database, len(nodes))
	}
}

func (u *ui) onSelect(uid string) {
	if uid == "" {
		return
	}
	n, ok := u.node(uid)
	if !ok {
		return
	}
	u.currentConn = n.connName
	u.query.setConnection(n.connName)

	switch n.kind {
	case nodeConn:
		u.currentDB, u.currentTable = "", ""
		u.tree.OpenBranch(uid)
		u.setStatus("已选择连接 %s", n.connName)
	case nodeDatabase:
		u.currentDB, u.currentTable = n.database, ""
		u.query.setDatabase(n.database)
		u.tree.OpenBranch(uid)
		u.setStatus("已选择数据库 %s", n.database)
	case nodeObject:
		u.currentDB, u.currentTable = n.database, n.table
		u.query.setDatabase(n.database)
		u.schema.load(n.connName, n.database, n.table)
		u.data.load(n.connName, n.database, n.table)
		u.tabs.SelectIndex(1)
		u.setStatus("已选择表 %s", n.table)
	}
}

func (u *ui) requireConnection() bool {
	if u.currentConn == "" {
		u.showInfo("提示", "请先在左侧选择一个连接")
		return false
	}
	return true
}

func (u *ui) showCreateDatabase() {
	if !u.requireConnection() {
		return
	}
	name := widget.NewEntry()
	charset := widget.NewEntry()
	charset.SetText("utf8mb4")
	collation := widget.NewEntry()
	ifNotExists := widget.NewCheck("使用 IF NOT EXISTS", nil)
	ifNotExists.SetChecked(true)

	dialog.ShowForm("新建数据库", "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("数据库名", name),
		widget.NewFormItem("字符集", charset),
		widget.NewFormItem("排序规则", collation),
		widget.NewFormItem("", ifNotExists),
	}, func(ok bool) {
		if !ok {
			return
		}
		conn := u.currentConn
		spec := domain.DatabaseSpec{
			Name: name.Text, Charset: charset.Text, Collation: collation.Text,
			IfNotExists: ifNotExists.Checked,
		}
		u.async(func() {
			script, err := u.core.PreviewCreateDatabase(context.Background(), conn, spec)
			if err != nil {
				u.showError(err)
				return
			}
			fyne.Do(func() { u.showScriptDialog("CREATE DATABASE 预览", conn, script) })
		})
	}, u.win)
}

func (u *ui) showAlterDatabase() {
	if !u.requireConnection() {
		return
	}
	if u.currentDB == "" {
		u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	name := widget.NewEntry()
	name.SetText(u.currentDB)
	charset := widget.NewEntry()
	collation := widget.NewEntry()

	dialog.ShowForm("修改数据库", "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("数据库名", name),
		widget.NewFormItem("新字符集", charset),
		widget.NewFormItem("新排序规则", collation),
	}, func(ok bool) {
		if !ok {
			return
		}
		conn, db := u.currentConn, name.Text
		spec := domain.DatabaseSpec{Name: db, Charset: charset.Text, Collation: collation.Text}
		u.async(func() {
			script, err := u.core.PreviewAlterDatabase(context.Background(), conn, db, spec)
			if err != nil {
				u.showError(err)
				return
			}
			fyne.Do(func() { u.showScriptDialog("ALTER DATABASE 预览", conn, script) })
		})
	}, u.win)
}

// showScriptDialog 是结构变更的统一确认入口：脚本只读预览 → 用户显式确认 → 执行。
func (u *ui) showScriptDialog(title, conn string, script domain.Script) {
	var b strings.Builder
	comment := "--"
	if script.Format == "mongo_command" {
		comment = "//"
	}
	for i, stmt := range script.Statements {
		if script.Format == "mongo_command" {
			fmt.Fprintf(&b, "%s 命令 %d\n%s\n\n", comment, i+1, stmt)
		} else {
			fmt.Fprintf(&b, "%s 语句 %d\n%s;\n\n", comment, i+1, stmt)
		}
	}
	for _, warn := range script.Warnings {
		fmt.Fprintf(&b, "%s 警告: %s\n", comment, warn)
	}
	if len(script.Rollback) > 0 {
		fmt.Fprintf(&b, "\n%s 回滚脚本（best-effort，执行前请人工复核）\n", comment)
		for _, stmt := range script.Rollback {
			if script.Format == "mongo_command" {
				fmt.Fprintf(&b, "%s %s\n", comment, stmt)
			} else {
				fmt.Fprintf(&b, "%s %s;\n", comment, stmt)
			}
		}
	}
	preview := widget.NewMultiLineEntry()
	preview.SetText(b.String())
	preview.Wrapping = fyne.TextWrapOff

	var d *dialog.CustomDialog
	execBtn := widget.NewButton("确认执行", func() {
		if len(script.Statements) == 0 {
			d.Hide()
			return
		}
		d.Hide()
		u.setStatus("正在执行结构变更 …")
		u.async(func() {
			result, err := u.core.ApplyScript(context.Background(), conn, script)
			if err != nil {
				u.showError(err)
				return
			}
			u.setStatus("结构变更执行成功，共 %d 条语句", len(result.Outcomes))
			u.showInfo("完成", "结构变更已执行，请刷新查看最新结构。")
			fyne.Do(func() {
				u.refreshConnections()
				if u.currentTable != "" {
					u.schema.load(conn, u.currentDB, u.currentTable)
				}
			})
		})
	})
	cancelBtn := widget.NewButton("取消", func() { d.Hide() })
	buttons := container.NewHBox(cancelBtn, execBtn)
	content := container.NewBorder(nil, buttons, nil, nil, container.NewScroll(preview))
	d = dialog.NewCustom(title, "关闭", content, u.win)
	d.Resize(fyne.NewSize(780, 500))
	d.Show()
}
