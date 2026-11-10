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

	"github.com/db-fly/db-fly/internal/domain"
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
		func(bool) fyne.CanvasObject { return newTreeRow(u) },
		func(uid widget.TreeNodeID, _ bool, o fyne.CanvasObject) {
			row, ok := o.(*treeRow)
			if !ok {
				return
			}
			row.uid = string(uid)
			if uid == "" {
				row.SetText("连接")
				return
			}
			if n, ok := u.node(string(uid)); ok {
				row.SetText(n.label)
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
		cat := u.categoryForConn(n.connName)
		nodes := make([]treeNode, 0, len(databases))
		for _, d := range databases {
			label := d.Name
			switch {
			case d.System:
				label += " (系统)"
			case cat == categoryKeyValue:
				label += " (逻辑库)"
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
		cat := u.categoryForConn(n.connName)
		nodes := make([]treeNode, 0, len(objects))
		for _, obj := range objects {
			label := obj.Name
			switch {
			case cat == categoryKeyValue && obj.Comment != "":
				label += " · " + obj.Comment
			case cat == categoryDocument:
				label += " (集合)"
			case obj.Type == domain.ObjectView:
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

// setObjectTarget 同步各视图的目标对象，使右键菜单动作不依赖上一次异步加载的结果。
func (u *ui) setObjectTarget(n treeNode) {
	u.currentConn, u.currentDB, u.currentTable = n.connName, n.database, n.table
	u.updateBreadcrumb()
	u.query.setDatabase(n.database)
	u.schema.setTarget(n.connName, n.database, n.table)
	u.data.setTarget(n.connName, n.database, n.table)
	u.keyView.setTarget(n.connName, n.database, n.table)
}

// onSelect 单击：只选中节点并同步面包屑 / 右键动作目标，不切换或加载任何视图。
func (u *ui) onSelect(uid string) {
	if uid == "" {
		return
	}
	n, ok := u.node(uid)
	if !ok {
		return
	}
	u.query.setConnection(n.connName)
	u.applyWorkspace(n.connName)
	u.setObjectTarget(n)
	workspaceFor(categoryOf(u.capsFor(n.connName))).OnSelect(u, n)
}

// onOpen 双击对象节点：按数据库类别打开该对象的独立页签。
func (u *ui) onOpen(uid string) {
	if uid == "" {
		return
	}
	n, ok := u.node(uid)
	if !ok {
		return
	}
	if n.kind != nodeObject {
		u.tree.OpenBranch(uid)
		return
	}
	u.query.setConnection(n.connName)
	u.applyWorkspace(n.connName)
	u.setObjectTarget(n)
	workspaceFor(categoryOf(u.capsFor(n.connName))).OnOpen(u, n)
}

func (u *ui) requireConnection() bool {
	if u.currentConn == "" {
		u.showInfo("提示", "请先在左侧选择一个连接")
		return false
	}
	return true
}

// showCreateDatabase 由连接节点右键或数据库节点右键触发，连接名显式传入，
// 不依赖「当前已选中哪个节点」。
func (u *ui) showCreateDatabase(conn string) {
	if conn == "" {
		u.showInfo("提示", "请先在左侧选择一个连接")
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

// showAlterDatabase 由数据库节点右键触发，直接作用于被右键的那个库。
func (u *ui) showAlterDatabase(conn, database string) {
	if conn == "" || database == "" {
		u.showInfo("提示", "请先在左侧选择一个数据库")
		return
	}
	name := widget.NewEntry()
	name.SetText(database)
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
		db := name.Text
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
	u.showScriptDialogWith(title, conn, script, true)
}

// showScriptDialogWith 的 reloadCurrent 为 false 时（重命名 / 删除对象）不重新加载
// 已被改名或删除的对象结构，避免执行成功后再次查询旧对象报错。
func (u *ui) showScriptDialogWith(title, conn string, script domain.Script, reloadCurrent bool) {
	var b strings.Builder
	comment := "--"
	isSQL := script.Format == "" || script.Format == "sql"
	switch {
	case script.Format == "mongo_command":
		comment = "//"
	case strings.Contains(script.Format, "redis"):
		comment = "#"
	}
	suffix := ";"
	if !isSQL {
		suffix = ""
	}
	for i, stmt := range script.Statements {
		if isSQL {
			fmt.Fprintf(&b, "%s 语句 %d\n%s;\n\n", comment, i+1, stmt)
		} else {
			fmt.Fprintf(&b, "%s 命令 %d\n%s\n\n", comment, i+1, stmt)
		}
	}
	for _, warn := range script.Warnings {
		fmt.Fprintf(&b, "%s 警告: %s\n", comment, warn)
	}
	if len(script.Rollback) > 0 {
		fmt.Fprintf(&b, "\n%s 回滚脚本（best-effort，执行前请人工复核）\n", comment)
		for _, stmt := range script.Rollback {
			fmt.Fprintf(&b, "%s %s%s\n", comment, stmt, suffix)
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
				target := u.currentTable
				if !reloadCurrent {
					u.currentTable = ""
					u.schema.setTarget(conn, u.currentDB, "")
					u.data.setTarget(conn, u.currentDB, "")
					u.keyView.setTarget(conn, u.currentDB, "")
				}
				u.updateBreadcrumb()
				u.refreshConnections()
				if reloadCurrent && target != "" && u.tabs != nil {
					u.tabs.refreshObject(conn, u.currentDB, target)
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
