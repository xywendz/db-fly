//go:build windows && cgo

// Package gui 是 Windows 桌面表现层，只依赖 app 用例层与 domain 契约。
package gui

import (
	"errors"
	"fmt"
	"sync"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	core "github.com/dbclient/dbclient/internal/app"
	"github.com/dbclient/dbclient/internal/domain"
	"github.com/dbclient/dbclient/internal/platform/audit"
	"github.com/dbclient/dbclient/internal/platform/config"
	"github.com/dbclient/dbclient/internal/platform/credential"
	_ "github.com/dbclient/dbclient/internal/provider/all"
)

type ui struct {
	core *core.App
	win  fyne.Window
	fapp fyne.App

	tree   *widget.Tree
	tabs   *container.AppTabs
	status *widget.Label

	treeMu   sync.RWMutex
	nodes    map[string]treeNode
	children map[string][]string

	currentConn  string
	currentDB    string
	currentTable string
	dark         bool

	query  *queryView
	schema *schemaView
	data   *dataView
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

	fapp := fyneapp.NewWithID("com.dbclient.desktop")
	u := newUI(a, fapp)
	if saved, err := cfg.Load(); err == nil {
		u.dark = saved.Preferences.Theme == "dark"
		if u.dark {
			fapp.Settings().SetTheme(theme.DarkTheme())
		}
	}
	u.win = fapp.NewWindow("DBClient")
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
	return u
}

func (u *ui) layout() fyne.CanvasObject {
	u.status = widget.NewLabel("就绪 · 数据目录 " + u.dataDir())
	u.tree = u.buildTree()

	left := container.NewBorder(
		widget.NewLabel("连接 / 数据库 / 对象"), nil, nil, nil,
		container.NewScroll(u.tree),
	)
	u.tabs = container.NewAppTabs(
		container.NewTabItem("SQL 查询", u.query.build()),
		container.NewTabItem("表结构", u.schema.build()),
		container.NewTabItem("表数据", u.data.build()),
	)
	split := container.NewHSplit(left, u.tabs)
	split.Offset = 0.24

	toolbar := container.NewHBox(
		widget.NewButton("连接管理", u.showConnections),
		widget.NewButton("新建数据库", u.showCreateDatabase),
		widget.NewButton("修改数据库", u.showAlterDatabase),
		widget.NewButton("刷新", func() { u.refreshConnections() }),
		layout.NewSpacer(),
		widget.NewButton("切换主题", u.toggleTheme),
	)
	bottom := container.NewHBox(u.status)
	return container.NewBorder(toolbar, bottom, nil, nil, split)
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
	out := make([]widget.TreeNodeID, 0, len(children))
	for _, c := range children {
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
