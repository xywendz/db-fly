//go:build windows && cgo

package gui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/dbclient/dbclient/internal/domain"
)

// refreshConnections 重新加载连接列表并刷新左侧树与查询页连接下拉框。
func (u *ui) refreshConnections() {
	list, err := u.core.ListConnections()
	if err != nil {
		u.showError(err)
		return
	}
	nodes := make([]treeNode, 0, len(list))
	for _, c := range list {
		nodes = append(nodes, treeNode{
			uid:      connUID(c.ID),
			kind:     nodeConn,
			label:    fmt.Sprintf("%s [%s]", c.Name, c.Kind),
			connID:   c.ID,
			connName: c.Name,
		})
	}
	u.treeMu.Lock()
	u.nodes = make(map[string]treeNode)
	u.children = make(map[string][]string)
	u.treeMu.Unlock()
	u.setChildren("", nodes)
	if u.tree != nil {
		u.tree.Refresh()
	}
	u.query.refreshConnections(list)
	u.setStatus("已加载 %d 个连接", len(nodes))
}

func (u *ui) showConnections() {
	list, err := u.core.ListConnections()
	if err != nil {
		u.showError(err)
		return
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		names = append(names, c.Name)
	}
	sel := widget.NewSelect(names, nil)
	if len(names) > 0 {
		sel.SetSelectedIndex(0)
	}

	var d *dialog.CustomDialog
	newBtn := widget.NewButton("新建", func() {
		d.Hide()
		u.showConnectionForm(nil)
	})
	editBtn := widget.NewButton("编辑", func() {
		c, ok := findConn(list, sel.Selected)
		if !ok {
			u.showInfo("提示", "请先选择一个连接")
			return
		}
		d.Hide()
		u.showConnectionForm(&c)
	})
	testBtn := widget.NewButton("测试", func() {
		name := sel.Selected
		if name == "" {
			return
		}
		u.setStatus("正在测试连接 %s …", name)
		u.async(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			info, err := u.core.TestSavedConnection(ctx, name)
			if err != nil {
				u.showError(err)
				return
			}
			u.setStatus("连接成功：%s %s", info.Kind, info.Version)
			u.showInfo("连接成功", fmt.Sprintf("%s %s (%s)", info.Kind, info.Version, info.Host))
		})
	})
	delBtn := widget.NewButton("删除", func() {
		name := sel.Selected
		if name == "" {
			return
		}
		dialog.ShowConfirm("删除连接", "确定删除连接「"+name+"」及其保存的凭证？", func(ok bool) {
			if !ok {
				return
			}
			if err := u.core.RemoveConnection(name); err != nil {
				u.showError(err)
				return
			}
			d.Hide()
			u.refreshConnections()
		}, u.win)
	})

	content := container.NewVBox(
		widget.NewLabel("已保存的连接："),
		sel,
		container.NewHBox(newBtn, editBtn, testBtn, delBtn),
	)
	d = dialog.NewCustom("连接管理", "关闭", content, u.win)
	d.Resize(fyne.NewSize(460, 210))
	d.Show()
}

func findConn(list []domain.ConnectionConfig, name string) (domain.ConnectionConfig, bool) {
	for _, c := range list {
		if c.Name == name {
			return c, true
		}
	}
	return domain.ConnectionConfig{}, false
}

func (u *ui) showConnectionForm(existing *domain.ConnectionConfig) {
	name := widget.NewEntry()
	kind := widget.NewSelect([]string{
		string(domain.KindMySQL), string(domain.KindSQLServer), string(domain.KindMongoDB),
	}, nil)
	host := widget.NewEntry()
	port := widget.NewEntry()
	user := widget.NewEntry()
	database := widget.NewEntry()
	tlsMode := widget.NewSelect([]string{"false", "true", "skip-verify", "preferred"}, nil)
	password := widget.NewPasswordEntry()
	timeout := widget.NewEntry()
	readOnly := widget.NewCheck("只读模式（拒绝写语句与结构变更）", nil)

	kind.SetSelected(string(domain.KindMySQL))
	host.SetText("127.0.0.1")
	port.SetText("3306")
	user.SetText("root")
	tlsMode.SetSelected("false")
	timeout.SetText("15")

	if existing != nil {
		name.SetText(existing.Name)
		kind.SetSelected(string(existing.Kind))
		host.SetText(existing.Host)
		port.SetText(strconv.Itoa(existing.Port))
		user.SetText(existing.User)
		database.SetText(existing.Database)
		if existing.TLSMode != "" {
			tlsMode.SetSelected(existing.TLSMode)
		}
		timeout.SetText(strconv.Itoa(existing.TimeoutSec))
		readOnly.SetChecked(existing.ReadOnly)
		password.SetPlaceHolder("留空表示不修改已保存的密码")
	}

	formConfig := func() (domain.ConnectionConfig, error) {
		p, err := strconv.Atoi(port.Text)
		if err != nil {
			return domain.ConnectionConfig{}, fmt.Errorf("端口必须是数字")
		}
		t, err := strconv.Atoi(timeout.Text)
		if err != nil {
			return domain.ConnectionConfig{}, fmt.Errorf("超时必须是数字")
		}
		cfg := domain.ConnectionConfig{
			Name: name.Text, Kind: domain.Kind(kind.Selected), Host: host.Text,
			Port: p, User: user.Text, Database: database.Text,
			TLSMode: tlsMode.Selected, TimeoutSec: t, ReadOnly: readOnly.Checked,
		}
		if existing != nil {
			cfg.ID = existing.ID
			cfg.Group = existing.Group
			cfg.Color = existing.Color
		}
		return cfg, nil
	}

	testBtn := widget.NewButton("测试当前填写的配置", func() {
		cfg, err := formConfig()
		if err != nil {
			u.showInfo("输入有误", err.Error())
			return
		}
		u.setStatus("正在测试 %s …", cfg.Name)
		u.async(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			info, err := u.core.TestConnection(ctx, cfg, password.Text)
			if err != nil {
				u.showError(err)
				return
			}
			u.showInfo("连接成功", fmt.Sprintf("%s %s", info.Kind, info.Version))
		})
	})

	items := []*widget.FormItem{
		widget.NewFormItem("名称", name),
		widget.NewFormItem("类型", kind),
		widget.NewFormItem("主机", host),
		widget.NewFormItem("端口", port),
		widget.NewFormItem("用户名", user),
		widget.NewFormItem("密码", password),
		widget.NewFormItem("默认数据库", database),
		widget.NewFormItem("TLS", tlsMode),
		widget.NewFormItem("连接超时(秒)", timeout),
		widget.NewFormItem("", readOnly),
		widget.NewFormItem("", testBtn),
	}
	d := dialog.NewForm("连接配置", "保存", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		cfg, err := formConfig()
		if err != nil {
			u.showInfo("输入有误", err.Error())
			return
		}
		pw := password.Text
		saved, err := u.core.SaveConnection(cfg, pw, pw != "" || existing == nil)
		if err != nil {
			u.showError(err)
			return
		}
		u.refreshConnections()
		u.setStatus("已保存连接 %s", saved.Name)
	}, u.win)
	d.Resize(fyne.NewSize(540, 620))
	d.Show()
}
