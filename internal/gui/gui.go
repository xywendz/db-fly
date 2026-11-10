//go:build windows && cgo

// Package gui 是 Windows 桌面表现层，只依赖 app 用例层与 domain 契约。
package gui

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	core "github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
	"github.com/db-fly/db-fly/internal/platform/audit"
	"github.com/db-fly/db-fly/internal/platform/config"
	"github.com/db-fly/db-fly/internal/platform/credential"
	_ "github.com/db-fly/db-fly/internal/provider/all"
)

type ui struct {
	core *core.App
	win  fyne.Window
	fapp fyne.App

	tree   *widget.Tree
	tabs   *tabManager
	status *widget.Label

	treeMu     sync.RWMutex
	nodes      map[string]treeNode
	children   map[string][]string
	treeFilter string
	treeSearch *widget.Entry

	currentConn  string
	currentDB    string
	currentTable string
	dark         bool

	category       dbCategory
	categoryBar    *widget.Label
	crumb          *widget.Label
	connLabel      *widget.Label
	workspaceReady bool
	workspaceConn  string

	// treeClick 处理树节点的右键与双击，状态在所有节点模板间共享。
	treeClick treeClick

	query   *queryView
	schema  *schemaView
	data    *dataView
	keyView *keyDetailView
}

// Run 启动 Windows GUI。
func Run() error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	cfg, err := config.New(dir)
	if err != nil {
		return err
	}
	secrets, err := credential.New(dir)
	if err != nil {
		return err
	}
	var logger audit.Logger = audit.Nop{}
	if l, lerr := audit.New(dir); lerr == nil {
		logger = l
	}
	a := core.New(cfg, secrets, logger)

	fapp := fyneapp.NewWithID("com.dbfly.desktop")
	u := newUI(a, fapp)
	if saved, err := cfg.Load(); err == nil {
		u.dark = saved.Preferences.Theme == "dark"
		if u.dark {
			fapp.Settings().SetTheme(theme.DarkTheme())
		}
	}
	u.win = fapp.NewWindow("db-fly")
	u.win.SetContent(u.layout())
	u.win.Resize(fyne.NewSize(1280, 820))
	u.win.SetOnClosed(func() { _ = a.Close() })
	u.refreshConnections()
	u.win.ShowAndRun()
	return nil
}

func newUI(a *core.App, fapp fyne.App) *ui {
	u := &ui{
		core:     a,
		fapp:     fapp,
		nodes:    make(map[string]treeNode),
		children: make(map[string][]string),
	}
	u.query = newQueryView(u)
	u.schema = newSchemaView(u)
	u.data = newDataView(u)
	u.keyView = newKeyDetailView(u)
	return u
}

func (u *ui) layout() fyne.CanvasObject {
	u.status = widget.NewLabel("就绪 · 数据目录 " + u.dataDir())
	u.tree = u.buildTree()

	// 四个「动作宿主」视图只构建一次：右键菜单中的脚本预览类动作复用它们，
	// 真正的数据展示由 tabManager 为每张表 / 每个查询创建独立实例。
	u.query.build()
	u.schema.build()
	u.data.build()
	u.keyView.build()

	u.treeSearch = widget.NewEntry()
	u.treeSearch.SetPlaceHolder("筛选连接 / 数据库 / 对象")
	u.treeSearch.OnChanged = func(text string) {
		u.treeMu.Lock()
		u.treeFilter = strings.TrimSpace(text)
		u.treeMu.Unlock()
		u.tree.Refresh()
	}
	left := container.NewBorder(
		container.NewVBox(widget.NewLabel("导航"), u.treeSearch, widget.NewSeparator()),
		nil, nil, nil, container.NewScroll(u.tree),
	)

	u.categoryBar = widget.NewLabel(categoryRelational.name())
	u.crumb = widget.NewLabel("未选择对象")
	u.connLabel = widget.NewLabel("连接: -")
	u.tabs = newTabManager(u)
	tabContent := u.tabs.build()
	crumbBar := container.NewHBox(
		widget.NewLabel("位置"), u.crumb, layout.NewSpacer(), u.categoryBar,
	)
	right := container.NewBorder(
		container.NewVBox(crumbBar, widget.NewSeparator()), nil, nil, nil, tabContent,
	)
	split := container.NewHSplit(left, right)
	split.Offset = 0.24

	// 数据库级动作已下沉到数据库/连接节点右键菜单，主工具栏只保留全局动作。
	toolbar := container.NewHBox(
		widget.NewButton("连接管理", u.showConnections),
		widget.NewButton("刷新连接树", func() { u.refreshConnections() }),
		widget.NewSeparator(),
		widget.NewButton("+ 新建查询", u.openNewQuery),
		widget.NewButton("导出向导", func() { u.openExportWizard(u.currentConn, u.currentDB, "") }),
		widget.NewButton("导入文件", u.showImportFile),
		widget.NewSeparator(),
		u.connLabel,
		layout.NewSpacer(),
		widget.NewButton("切换主题", u.toggleTheme),
	)
	bottom := container.NewHBox(u.status)
	return container.NewBorder(toolbar, bottom, nil, nil, split)
}

// updateBreadcrumb 在面包屑中展示当前连接 / 数据库 / 对象。
func (u *ui) updateBreadcrumb() {
	if u.crumb == nil {
		return
	}
	parts := make([]string, 0, 3)
	if u.currentConn != "" {
		parts = append(parts, u.currentConn)
	}
	if u.currentDB != "" {
		parts = append(parts, u.currentDB)
	}
	if u.currentTable != "" {
		parts = append(parts, u.currentTable)
	}
	if len(parts) == 0 {
		u.crumb.SetText("未选择对象")
	} else {
		u.crumb.SetText(strings.Join(parts, "  ›  "))
	}
	if u.connLabel != nil {
		if u.currentConn == "" {
			u.connLabel.SetText("连接: -")
		} else {
			u.connLabel.SetText("连接: " + u.currentConn)
		}
	}
}

func (u *ui) dataDir() string {
	dir, _ := config.DefaultDir()
	return dir
}

func (u *ui) toggleTheme() {
	u.dark = !u.dark
	name := "light"
	if u.dark {
		u.fapp.Settings().SetTheme(theme.DarkTheme())
		name = "dark"
	} else {
		u.fapp.Settings().SetTheme(theme.LightTheme())
	}
	prefs, err := u.core.Preferences()
	if err != nil {
		u.setStatus("读取偏好失败: %s", domain.UserMessage(err))
		return
	}
	prefs.Theme = name
	if err := u.core.SavePreferences(prefs); err != nil {
		u.setStatus("保存主题偏好失败: %s", domain.UserMessage(err))
	}
}

// setStatus 线程安全地更新状态栏。
func (u *ui) setStatus(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fyne.Do(func() { u.status.SetText(text) })
}

func (u *ui) showError(err error) {
	if err == nil {
		return
	}
	msg := domain.UserMessage(err)
	fyne.Do(func() { dialog.ShowError(errors.New(msg), u.win) })
}

func (u *ui) showInfo(title, message string) {
	fyne.Do(func() { dialog.ShowInformation(title, message, u.win) })
}

// async 把耗时 IO 放到后台，并捕获 panic 避免单个连接异常导致整个 GUI 退出。
func (u *ui) async(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				u.setStatus("内部错误: %v", r)
				u.showError(fmt.Errorf("内部错误: %v", r))
			}
		}()
		fn()
	}()
}

func (u *ui) node(uid string) (treeNode, bool) {
	u.treeMu.RLock()
	defer u.treeMu.RUnlock()
	n, ok := u.nodes[uid]
	return n, ok
}

func (u *ui) childUIDs(uid string) []widget.TreeNodeID {
	u.treeMu.RLock()
	defer u.treeMu.RUnlock()
	children := u.children[uid]
	filter := strings.ToLower(u.treeFilter)
	out := make([]widget.TreeNodeID, 0, len(children))
	for _, c := range children {
		if filter != "" {
			n, ok := u.nodes[c]
			if !ok || !strings.Contains(strings.ToLower(n.label), filter) {
				continue
			}
		}
		out = append(out, widget.TreeNodeID(c))
	}
	return out
}

func (u *ui) setChildren(uid string, nodes []treeNode) {
	u.treeMu.Lock()
	for _, n := range nodes {
		u.nodes[n.uid] = n
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.uid)
	}
	u.children[uid] = ids
	u.treeMu.Unlock()
}
