//go:build windows && cgo

package gui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	core "github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/platform/credential"
)

func TestTreeClickDoublePress(t *testing.T) {
	var c treeClick
	if c.doublePress("a") {
		t.Fatal("第一次按下不应识别为双击")
	}
	if !c.doublePress("a") {
		t.Fatal("同一节点的第二次按下应识别为双击")
	}
	if !c.consumeTap() {
		t.Fatal("双击后的第二次单击应被抑制")
	}
	if c.consumeTap() {
		t.Fatal("只应抑制一次单击")
	}
	if c.doublePress("b") {
		t.Fatal("不同节点不应识别为双击")
	}
}

func TestDataViewStructuredConditions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	v := newDataView(&ui{})
	v.build()
	v.columns = []domain.ColumnInfo{{Name: "id"}, {Name: "name"}, {Name: "age"}}
	v.refreshFieldOptions()

	first := v.filterRows[0]
	first.field.SetSelected("name")
	first.op.SetSelected("=")
	first.value.SetText("Ann")
	v.addFilterRow()
	second := v.filterRows[1]
	second.join.SetSelected("OR")
	second.field.SetSelected("age")
	second.op.SetSelected(">")
	second.value.SetText("18")

	conditions := v.conditions()
	if len(conditions) != 2 || conditions[0].Join != "" ||
		conditions[1].Join != "OR" || conditions[1].Column != "age" {
		t.Fatalf("结构化过滤条件不正确: %+v", conditions)
	}
	if preview := v.preview.Text; !strings.Contains(preview, "WHERE name = 'Ann' OR age > '18'") {
		t.Fatalf("条件预览不正确: %s", preview)
	}

	v.addOrderRow()
	v.orderRows[0].field.SetSelected("id")
	v.orderRows[0].dir.SetSelected("降序")
	terms := v.orderTerms()
	if len(terms) != 1 || terms[0].Column != "id" || !terms[0].Desc {
		t.Fatalf("结构化排序不正确: %+v", terms)
	}

	v.removeFilterRow(second)
	if len(v.filterRows) != 1 || v.filterRows[0].join.Visible() {
		t.Fatal("删除条件后首行应恢复 WHERE 前缀")
	}
	v.resetConditions()
	if len(v.filterRows) != 1 || len(v.orderRows) != 0 || len(v.conditions()) != 0 {
		t.Fatal("重置条件后应只剩一个空过滤行")
	}
	if v.pageSize() <= 0 {
		t.Fatal("每页行数必须为正数")
	}
}

func TestInlineCellStaging(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	u := &ui{status: widget.NewLabel("")}
	v := newDataView(u)
	v.build()
	v.category = categoryRelational
	v.caps = domain.CapabilitySet{domain.CapDataEdit: true}
	v.grid.pages = []domain.ResultPage{{
		Columns: []domain.Column{{Name: "id", Type: "BIGINT"}, {Name: "name", Type: "VARCHAR(32)"}},
		Rows:    []domain.Row{{domain.NewTextCell("1"), domain.NewTextCell("Ann")}},
	}}
	v.grid.active = 0
	v.rowCount = 1

	if !v.commitBtn.Disabled() {
		t.Fatal("无待提交变更时提交按钮应禁用")
	}
	v.stageCell(widget.TableCellID{Row: 1, Col: 1}, "Betty")
	if len(v.pending) != 1 || v.commitBtn.Disabled() {
		t.Fatalf("内联编辑后应暂存并启用提交: %+v", v.pending)
	}
	if text, ok := v.cellDisplay(widget.TableCellID{Row: 1, Col: 1}); !ok || text != "✎ Betty" {
		t.Fatalf("已修改单元格应带标记: %q %v", text, ok)
	}

	v.addRecord()
	if len(v.newRows) != 1 || v.grid.extraRows() != 1 {
		t.Fatal("新增记录应在网格中追加一行")
	}
	v.stageCell(widget.TableCellID{Row: 2, Col: 1}, "Cindy")
	if v.newRows[0][1] != "Cindy" {
		t.Fatalf("新增行的内联编辑应写入暂存: %+v", v.newRows)
	}

	v.grid.selected = widget.TableCellID{Row: 1, Col: 0}
	v.grid.hasSelection = true
	v.deleteRecord()
	if !v.pendingDeletes[0] {
		t.Fatal("删除选中记录应写入删除标记")
	}
	if v.stagedCount() != 3 {
		t.Fatalf("待提交项统计不正确: %d", v.stagedCount())
	}

	v.revert()
	if v.stagedCount() != 0 || len(v.newRows) != 0 || !v.commitBtn.Disabled() {
		t.Fatal("回退后应清空全部暂存")
	}
}

func TestFilterBuilderCollapse(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	v := newDataView(&ui{status: widget.NewLabel("")})
	v.build()
	if !v.builder.Visible() {
		t.Fatal("条件卡片默认应展开")
	}
	v.toggleBuilder()
	if v.builder.Visible() {
		t.Fatal("点击折叠后条件卡片应隐藏")
	}
	if !v.filterToggle.Visible() || !strings.Contains(v.filterToggle.Text, "▼") {
		t.Fatal("折叠开关必须常驻且显示为可展开状态")
	}
	v.toggleBuilder()
	if !v.builder.Visible() || !strings.Contains(v.filterToggle.Text, "▲") {
		t.Fatal("再次点击折叠开关应重新展开条件卡片")
	}
}

func TestEditableCellSwitchesModes(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	c := newEditableCell()
	if test.WidgetRenderer(c) == nil {
		t.Fatal("editableCell 应可正常渲染")
	}
	c.ShowLabel("Ann")
	if !c.Label.Visible() || c.Entry.Visible() || c.Label.Text != "Ann" {
		t.Fatal("普通状态应显示 Label")
	}
	c.ShowEditor("Ann")
	if c.Label.Visible() || !c.Entry.Visible() || c.Entry.Text != "Ann" {
		t.Fatal("编辑状态应在原单元格显示 Entry")
	}
	c.Entry.SetText("Betty")
	c.ShowEditor("Ann")
	if c.Entry.Text != "Betty" {
		t.Fatal("刷新不应覆盖用户正在输入的内容")
	}
	c.ShowLabel("Betty")
	if !c.Label.Visible() || c.Entry.Visible() || c.Label.Text != "Betty" {
		t.Fatal("提交后应切回 Label 并显示新值")
	}
}

func TestQueryEditorHelpers(t *testing.T) {
	formatted := formatSQL("SELECT * FROM users WHERE id = 1 ORDER BY id")
	if !strings.Contains(formatted, "\nFROM\n") ||
		!strings.Contains(formatted, "\nWHERE\n") ||
		!strings.Contains(formatted, "\nORDER BY\n") {
		t.Fatalf("格式化 SQL 未按子句换行: %q", formatted)
	}
	if got := formatSQL(`{"a":1}`); got != `{"a":1}` {
		t.Fatal("非 SQL 文本不应被改写")
	}
	if got := lineNumberGutter("a\nb\nc"); got != "1\n2\n3" {
		t.Fatalf("行号槽不正确: %q", got)
	}
}

func TestParseColumnType(t *testing.T) {
	cases := []struct {
		raw                  string
		length               int64
		base, size, decimals string
		unsigned, zerofill   bool
	}{
		{"varchar(32)", 0, "varchar", "32", "", false, false},
		{"decimal(10,2) unsigned", 0, "decimal", "10", "2", true, false},
		{"bigint(20) unsigned zerofill", 0, "bigint", "20", "", true, true},
		{"datetime(3)", 0, "datetime", "3", "", false, false},
		{"varchar", 64, "varchar", "64", "", false, false},
	}
	for _, tc := range cases {
		got := parseColumnType(tc.raw, tc.length)
		if got.Base != tc.base || got.Length != tc.size || got.Decimals != tc.decimals ||
			got.Unsigned != tc.unsigned || got.Zerofill != tc.zerofill {
			t.Fatalf("解析 %q 失败: %+v", tc.raw, got)
		}
	}
}

func TestSchemaColumnGrid(t *testing.T) {
	c := domain.ColumnInfo{
		Name: "id", RawType: "bigint(20) unsigned", Nullable: false,
		PrimaryKey: true, Comment: "主键",
	}
	want := []string{"id", "bigint", "20", "", "✓", "", "PK 1", "主键"}
	for col, expected := range want {
		if got := schemaColumnCell(c, col); got != expected {
			t.Fatalf("第 %d 列应为 %q，实际 %q", col, expected, got)
		}
	}
}

func TestSchemaViewLayout(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	u := &ui{status: widget.NewLabel("")}
	v := newSchemaView(u)
	v.build()
	caps := domain.CapabilitySet{
		domain.CapAlterTable: true, domain.CapShowCreate: true,
		domain.CapCreateTable: true, domain.CapDropObject: true,
	}
	v.setCategory(categoryRelational, caps)
	if len(v.tabs.Items) != 6 {
		t.Fatalf("关系型表结构页应有 6 个页签，实际 %d", len(v.tabs.Items))
	}
	if v.fieldSplit == nil || v.fieldSplit.Offset <= 0 || v.fieldSplit.Offset >= 1 {
		t.Fatal("字段页应使用可拖动上下分割的网格 + 属性面板")
	}
	if !v.colToolbar.Visible() || v.idxToolbar.Visible() {
		t.Fatal("字段页应显示字段工具条")
	}
	v.tabs.Select(v.idxItem)
	if v.colToolbar.Visible() || !v.idxToolbar.Visible() {
		t.Fatal("切换到索引页应切换上下文工具条")
	}

	v.columns = []domain.ColumnInfo{{
		Name: "id", RawType: "bigint(20) unsigned", Nullable: false,
		PrimaryKey: true, AutoIncrement: true,
	}}
	v.colTable.Select(widget.TableCellID{Row: 1, Col: 0})
	if !strings.Contains(v.detailType.Text, "unsigned") ||
		!v.detailAuto.Checked || !v.detailKey.Checked || !v.detailUnsign.Checked {
		t.Fatalf("字段属性面板未随选中字段更新: %q", v.detailType.Text)
	}
	if v.detailType.Disabled() == false {
		t.Fatal("字段属性面板应为只读")
	}
}

func TestTableDesignerGrid(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	d := &tableDesigner{
		u: &ui{status: widget.NewLabel("")}, conn: "dev", database: "app",
		category: categoryRelational, fieldSel: -1, indexSel: -1,
	}
	d.name = widget.NewEntry()
	d.columns = []domain.ColumnSpec{{Name: "id", Type: "BIGINT", PrimaryKey: true, AutoIncrement: true}}
	d.buildFieldsTab()
	d.buildIndexesTab()
	d.buildOptionsTab()

	if got := d.columnCell(widget.TableCellID{Row: 0, Col: 0}); got != "字段" {
		t.Fatalf("字段网格表头应与表结构页一致: %s", got)
	}
	d.addColumn()
	if len(d.columns) != 2 || d.columnCell(widget.TableCellID{Row: 2, Col: 0}) != "column_2" {
		t.Fatalf("添加字段失败: %+v", d.columns)
	}
	d.fName.SetText("age")
	d.fType.SetText("INT")
	if d.columns[1].Name != "age" || d.columns[1].Type != "INT" {
		t.Fatalf("属性表单未写回字段定义: %+v", d.columns[1])
	}
	d.addIndex()
	d.iName.SetText("idx_age")
	d.iCols.SetText("age")
	if len(d.indexes) != 1 || d.indexes[0].Name != "idx_age" || len(d.indexes[0].Columns) != 1 {
		t.Fatalf("索引表单未写回索引定义: %+v", d.indexes)
	}
	d.removeColumn()
	if len(d.columns) != 1 {
		t.Fatalf("删除字段失败: %+v", d.columns)
	}
}

// TestTreeRowRenders 验证扩展的树节点模板能被 Fyne 正常渲染（右键 / 双击依赖它）。
func TestTreeRowRenders(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	u := &ui{}
	row := newTreeRow(u)
	row.uid = "obj|c|db|t"
	row.SetText("users")
	if test.WidgetRenderer(row) == nil {
		t.Fatal("custom tree row has no renderer")
	}

	tree := widget.NewTree(
		func(widget.TreeNodeID) []widget.TreeNodeID { return []widget.TreeNodeID{"child"} },
		func(widget.TreeNodeID) bool { return false },
		func(bool) fyne.CanvasObject { return newTreeRow(u) },
		func(id widget.TreeNodeID, _ bool, o fyne.CanvasObject) {
			o.(*treeRow).uid = string(id)
			o.(*treeRow).SetText(string(id))
		},
	)
	if test.WidgetRenderer(tree) == nil {
		t.Fatal("tree has no renderer")
	}
}

func newTestUI(t *testing.T) (*ui, fyne.App) {
	t.Helper()
	app := test.NewApp()
	t.Cleanup(app.Quit)
	dir := t.TempDir()
	cfg, err := config.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := credential.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := core.New(cfg, secrets, audit.Nop{})
	for _, kind := range []domain.Kind{domain.KindMySQL, domain.KindMongoDB, domain.KindRedis} {
		if _, err := a.SaveConnection(domain.ConnectionConfig{
			Name: string(kind), Kind: kind, Host: "127.0.0.1",
			Port: domain.DefaultPort(kind), User: "u", TimeoutSec: 5,
		}, "", true); err != nil {
			t.Fatal(err)
		}
	}
	u := newUI(a, app)
	u.win = app.NewWindow("test")
	u.win.SetContent(u.layout())
	u.refreshConnections()
	return u, app
}

// TestWorkspaceLayouts 验证三种数据库类别都能装配各自的工作区与对象菜单。
func TestWorkspaceLayouts(t *testing.T) {
	u, _ := newTestUI(t)

	if u.tabs == nil || u.tabs.len() < 1 {
		t.Fatal("启动后应有一个默认查询页签")
	}
	for _, tc := range []struct {
		conn string
		cat  dbCategory
	}{
		{"mysql", categoryRelational},
		{"mongodb", categoryDocument},
		{"redis", categoryKeyValue},
	} {
		u.applyWorkspace(tc.conn)
		if u.category != tc.cat {
			t.Fatalf("%s: category = %d, want %d", tc.conn, u.category, tc.cat)
		}
		w := workspaceFor(tc.cat)
		if w.Category() != tc.cat {
			t.Fatalf("%s: workspace 分派错误", tc.conn)
		}
		node := treeNode{
			uid: "obj|id|db|t", kind: nodeObject, connName: tc.conn,
			database: "db", table: "t", connID: "id",
		}
		if len(w.ObjectMenu(u, node, u.capsFor(tc.conn))) == 0 {
			t.Fatalf("%s: 对象菜单为空", tc.conn)
		}
		if len(w.DatabaseMenu(u, node, u.capsFor(tc.conn))) == 0 {
			t.Fatalf("%s: 数据库菜单为空", tc.conn)
		}
	}

	u.applyWorkspace("mysql")
	caps := u.capsFor("mysql")
	w := workspaceFor(categoryRelational)
	dbNode := treeNode{uid: "db|id|app", kind: nodeDatabase, connName: "mysql", database: "app", connID: "id"}
	var foundAlter, foundCreate bool
	for _, item := range w.DatabaseMenu(u, dbNode, caps) {
		switch item.Label {
		case "修改数据库":
			foundAlter = !item.Disabled
		case "新建数据库":
			foundCreate = !item.Disabled
		}
	}
	if !foundAlter || !foundCreate {
		t.Fatalf("数据库右键缺少库级功能: alter=%v create=%v", foundAlter, foundCreate)
	}
	connNode := treeNode{uid: "conn|id", kind: nodeConn, connName: "mysql", connID: "id"}
	if !hasMenuLabel(w.ConnectionMenu(u, connNode, caps), "新建数据库") {
		t.Fatal("连接右键应提供新建数据库")
	}
}

func TestTabManagerPerTable(t *testing.T) {
	u, _ := newTestUI(t)
	before := u.tabs.len()
	node := treeNode{
		uid: "obj|mysql|app|admins", kind: nodeObject,
		connID: "mysql", connName: "mysql", database: "app", table: "admins",
	}
	u.currentConn, u.currentDB, u.currentTable = node.connName, node.database, node.table
	e1 := u.tabs.openTable(node)
	if u.tabs.len() != before+1 || e1.data == nil || e1.schema == nil {
		t.Fatal("双击应为该表创建一个独立页签（含数据与结构视图）")
	}
	// 同一张表重复打开只激活，不新增页签。
	e2 := u.tabs.openTable(node)
	if e2 != e1 || u.tabs.len() != before+1 {
		t.Fatal("每张表只应拥有一个页签")
	}
	// 不同表各自一个页签。
	other := node
	other.table, other.uid = "users", "obj|mysql|app|users"
	u.tabs.openTable(other)
	if u.tabs.len() != before+2 {
		t.Fatal("不同表应各自拥有独立页签")
	}
	// 新建表打开独立页签且不弹窗。
	u.tabs.openCreateTable("mysql", "app", categoryRelational)
	if u.tabs.len() != before+3 {
		t.Fatal("新建表应打开新的页签")
	}
	// 关闭后条目被清理。
	u.tabs.strip.Remove(e1.id)
	if u.tabs.len() != before+2 || u.tabs.entries[e1.id] != nil {
		t.Fatal("关闭页签后应清理条目")
	}
}

func hasMenuLabel(items []*fyne.MenuItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}
