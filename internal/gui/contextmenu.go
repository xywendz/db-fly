//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	core "github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/export"
)

// treeClick 在所有树节点模板间共享，用于识别双击并抑制双击后的第二次单击。
type treeClick struct {
	mu         sync.Mutex
	lastUID    string
	lastPress  time.Time
	suppress   int
	suppressAt time.Time
}

func (c *treeClick) doublePress(uid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.lastUID == uid && now.Sub(c.lastPress) < 400*time.Millisecond {
		c.lastUID, c.lastPress = "", time.Time{}
		c.suppress = 1
		c.suppressAt = now
		return true
	}
	c.lastUID, c.lastPress = uid, now
	return false
}

func (c *treeClick) consumeTap() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.suppress > 0 && time.Since(c.suppressAt) < time.Second {
		c.suppress--
		return true
	}
	c.suppress = 0
	return false
}

// treeRow 是连接树的节点模板，额外支持右键菜单与双击打开数据。
// 通过实现 Tapped 保持 Fyne 原生树节点的单击选中行为。
type treeRow struct {
	widget.Label
	u   *ui
	uid string
}

var (
	_ fyne.Tappable          = (*treeRow)(nil)
	_ fyne.SecondaryTappable = (*treeRow)(nil)
	_ desktop.Mouseable      = (*treeRow)(nil)
)

func newTreeRow(u *ui) *treeRow {
	r := &treeRow{u: u}
	r.ExtendBaseWidget(r)
	r.Wrapping = fyne.TextWrapOff
	r.Truncation = fyne.TextTruncateEllipsis
	return r
}

func (r *treeRow) Tapped(*fyne.PointEvent) {
	if r.u.treeClick.consumeTap() {
		return
	}
	if r.uid == "" {
		return
	}
	r.u.tree.Select(r.uid)
	if canvas := fyne.CurrentApp().Driver().CanvasForObject(r.u.tree); canvas != nil &&
		!fyne.CurrentDevice().IsMobile() {
		canvas.Focus(r.u.tree)
	}
}

func (r *treeRow) TappedSecondary(e *fyne.PointEvent) {
	if r.uid == "" {
		return
	}
	r.u.tree.Select(r.uid)
	r.u.showNodeMenu(r.uid, e.AbsolutePosition)
}

func (r *treeRow) MouseDown(e *desktop.MouseEvent) {
	if e.Button != desktop.MouseButtonPrimary || r.uid == "" {
		return
	}
	if r.u.treeClick.doublePress(r.uid) {
		r.u.onOpen(r.uid)
	}
}

func (r *treeRow) MouseUp(*desktop.MouseEvent) {}

func objRef(n treeNode) domain.ObjectRef {
	return domain.ObjectRef{Database: n.database, Name: n.table}
}

// showNodeMenu 按节点类型与数据库类别生成对应的右键菜单。
func (u *ui) showNodeMenu(uid string, pos fyne.Position) {
	n, ok := u.node(uid)
	if !ok {
		return
	}
	caps := u.capsFor(n.connName)
	w := workspaceFor(categoryOf(caps))
	var items []*fyne.MenuItem
	switch n.kind {
	case nodeObject:
		items = w.ObjectMenu(u, n, caps)
	case nodeDatabase:
		items = w.DatabaseMenu(u, n, caps)
	default:
		items = w.ConnectionMenu(u, n, caps)
	}
	if len(items) == 0 {
		return
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), u.win.Canvas(), pos)
}

func menuItem(label string, enabled bool, action func()) *fyne.MenuItem {
	item := fyne.NewMenuItem(label, action)
	item.Disabled = !enabled
	return item
}

// refreshChildren 无论分支是否已展开都重新拉取子节点。
func (u *ui) refreshChildren(uid string) {
	u.async(func() {
		u.loadChildren(uid)
		fyne.Do(func() { u.tree.Refresh() })
	})
}

// showDatabaseStructure 汇总库内所有表的字段定义，纯只读展示。
func (u *ui) showDatabaseStructure(conn, database string) {
	u.setStatus("正在读取数据库 %s 的结构 …", database)
	u.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		objects, err := u.core.ListObjects(ctx, conn, database, "")
		if err != nil {
			u.showError(err)
			return
		}
		var b strings.Builder
		b.WriteString("-- 数据库 " + database + " 结构快照\n")
		for _, obj := range objects {
			schema, err := u.core.Describe(ctx, conn, domain.ObjectRef{Database: database, Name: obj.Name})
			if err != nil {
				fmt.Fprintf(&b, "\n-- %s: 读取失败 %s\n", obj.Name, domain.UserMessage(err))
				continue
			}
			fmt.Fprintf(&b, "\n-- %s (%d 字段, %d 索引)\n", obj.Name, len(schema.Columns), len(schema.Indexes))
			for _, col := range schema.Columns {
				typ := col.RawType
				if typ == "" {
					typ = col.DataType
				}
				fmt.Fprintf(&b, "   %-28s %-24s nullable=%v\n", col.Name, typ, col.Nullable)
			}
			if ddl := strings.TrimSpace(schema.DDL); ddl != "" {
				b.WriteString(ddl)
				b.WriteString("\n")
			}
		}
		text := b.String()
		fyne.Do(func() {
			entry := widget.NewMultiLineEntry()
			entry.Wrapping = fyne.TextWrapOff
			entry.SetText(text)
			scroll := container.NewScroll(entry)
			scroll.SetMinSize(fyne.NewSize(820, 520))
			dialog.ShowCustom("数据库结构 · "+database, "关闭", scroll, u.win)
		})
	})
}

func (u *ui) openObjectData(n treeNode) {
	u.setObjectTarget(n)
	u.selectTab(tabData)
}

func (u *ui) openKeyDetail(n treeNode) {
	u.setObjectTarget(n)
	u.selectTab(tabKeyDetail)
}

func (u *ui) renameObject(n treeNode) {
	label := "修改" + categoryOf(u.capsFor(n.connName)).objectName() + "名"
	name := widget.NewEntry()
	name.SetText(n.table)
	dialog.ShowForm(label, "生成脚本", "取消", []*widget.FormItem{
		widget.NewFormItem("新名称", name),
	}, func(ok bool) {
		if !ok {
			return
		}
		u.async(func() {
			script, err := u.core.PreviewRenameTable(context.Background(), n.connName, objRef(n), name.Text)
			if err != nil {
				u.showError(err)
				return
			}
			fyne.Do(func() { u.showScriptDialogWith(label+" 预览", n.connName, script, false) })
		})
	}, u.win)
}

func (u *ui) tableAction(n treeNode, action domain.TableAction) {
	u.data.setTarget(n.connName, n.database, n.table)
	u.data.showTableActionFor(action, n.connName, n.database, n.table)
}

// alterObjectStructure 打开该表的独立页签并切到「表结构」子页签。
func (u *ui) alterObjectStructure(n treeNode) {
	u.setObjectTarget(n)
	u.selectTab(tabSchema)
}

func (u *ui) showObjectDefinition(n treeNode) {
	u.schema.setTarget(n.connName, n.database, n.table)
	u.schema.showDDL()
}

func (u *ui) modifyCollectionValidator(n treeNode) {
	u.setObjectTarget(n)
	u.schema.showCollectionValidator()
}

func (u *ui) deleteObject(n treeNode) {
	u.schema.setTarget(n.connName, n.database, n.table)
	u.schema.showDropTable()
}

func (u *ui) deleteKey(n treeNode) {
	u.keyView.setTarget(n.connName, n.database, n.table)
	u.keyView.deleteKey()
}

// exportObjectData 重新查询对象数据并写入文件；CSV 导出第一个结果集，JSON 导出完整结果。
func (u *ui) exportObjectData(n treeNode) {
	path := widget.NewEntry()
	home, _ := os.UserHomeDir()
	path.SetText(filepath.Join(home, "Downloads", safeFileName(n.table)+".csv"))
	format := widget.NewSelect([]string{"CSV", "JSON"}, nil)
	format.SetSelected("CSV")
	limit := widget.NewEntry()
	limit.SetText("10000")
	dialog.ShowForm("导出数据 · "+n.table, "导出", "取消", []*widget.FormItem{
		widget.NewFormItem("文件路径", path),
		widget.NewFormItem("格式", format),
		widget.NewFormItem("最大行数", limit),
	}, func(ok bool) {
		if !ok {
			return
		}
		maxRows, err := strconv.Atoi(strings.TrimSpace(limit.Text))
		if err != nil || maxRows <= 0 {
			u.showInfo("输入有误", "最大行数必须是正整数")
			return
		}
		target := strings.TrimSpace(path.Text)
		if target == "" {
			u.showInfo("输入有误", "请填写导出文件路径")
			return
		}
		asJSON := format.Selected == "JSON"
		u.setStatus("正在导出 %s …", n.table)
		u.async(func() {
			sink := &core.CollectSink{}
			out, err := u.core.Execute(context.Background(), n.connName, domain.Query{
				Ref: objRef(n), Database: n.database, MaxRows: maxRows,
			}, sink)
			if err != nil {
				u.showError(err)
				return
			}
			f, err := os.Create(target)
			if err != nil {
				u.showError(err)
				return
			}
			defer f.Close()
			if asJSON {
				err = export.JSON(f, out)
			} else if len(out.Pages) > 0 {
				err = export.CSV(f, out.Pages[0])
			}
			if err != nil {
				u.showError(err)
				return
			}
			u.setStatus("已导出 %d 行到 %s", out.Summary.Rows, target)
			u.showInfo("完成", fmt.Sprintf("已导出 %d 行到 %s", out.Summary.Rows, target))
		})
	}, u.win)
}

func safeFileName(name string) string {
	name = strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_").Replace(name)
	if strings.TrimSpace(name) == "" {
		return "db-fly_export"
	}
	return name
}

// showCreateKey 是键值型数据库的「新建键」入口，生成 SET 命令后走统一脚本确认流程。
func (u *ui) showCreateKey() {
	if !u.requireConnection() {
		return
	}
	if u.currentDB == "" {
		u.showInfo("提示", "请先在左侧选择一个逻辑库")
		return
	}
	name := widget.NewEntry()
	name.SetPlaceHolder("键名，如 user:1")
	value := widget.NewEntry()
	value.SetPlaceHolder("初始值（字符串）")
	dialog.ShowForm("新建键", "生成命令", "取消", []*widget.FormItem{
		widget.NewFormItem("键名", name),
		widget.NewFormItem("初始值", value),
	}, func(ok bool) {
		if !ok {
			return
		}
		key := strings.TrimSpace(name.Text)
		if key == "" {
			u.showInfo("输入有误", "键名不能为空")
			return
		}
		initial := value.Text
		conn := u.currentConn
		def := domain.TableDefinition{
			Ref: domain.ObjectRef{Database: u.currentDB, Name: key},
			Columns: []domain.ColumnSpec{
				{Name: "value", Type: "string", Default: &initial},
			},
		}
		u.async(func() {
			script, err := u.core.PreviewCreateTable(context.Background(), conn, def)
			if err != nil {
				u.showError(err)
				return
			}
			fyne.Do(func() { u.showScriptDialog("SET 预览", conn, script) })
		})
	}, u.win)
}
