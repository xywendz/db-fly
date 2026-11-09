//go:build windows && cgo

package gui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/db-fly/db-fly/internal/platform/config"
)

type queryPick struct {
	Text       string
	Connection string
	Database   string
	Collection string
	Language   string
}

func showSaveFavorite(u *ui, conn, database, collection, language, text string) {
	if strings.TrimSpace(text) == "" {
		u.showInfo("提示", "没有可保存的查询内容")
		return
	}
	name := widget.NewEntry()
	name.SetPlaceHolder("例如：今日活跃用户")
	dialog.ShowForm("保存收藏", "保存", "取消", []*widget.FormItem{
		widget.NewFormItem("名称", name),
	}, func(ok bool) {
		if !ok {
			return
		}
		saved, err := u.core.SaveSavedQuery(config.SavedQuery{
			Name: name.Text, Connection: conn, Database: database,
			Collection: collection, Language: language, Text: text,
		})
		if err != nil {
			u.showError(err)
			return
		}
		u.showInfo("完成", "已保存收藏 "+saved.Name)
	}, u.win)
}

func showQueryLibrary(u *ui, onPick func(queryPick)) {
	favorites, err := u.core.ListSavedQueries()
	if err != nil {
		u.showError(err)
		return
	}
	history, err := u.core.ListHistory(50)
	if err != nil {
		u.showError(err)
		return
	}

	var d dialog.Dialog
	pick := func(p queryPick) {
		onPick(p)
		if d != nil {
			d.Hide()
		}
	}

	favoriteList := container.NewVBox()
	for _, item := range favorites {
		saved := item
		label := fmt.Sprintf("%s  [%s]  %s", saved.Name, saved.Language, shortText(saved.Text, 56))
		open := widget.NewButton(label, func() {
			pick(queryPick{
				Text: saved.Text, Connection: saved.Connection, Database: saved.Database,
				Collection: saved.Collection, Language: saved.Language,
			})
		})
		remove := widget.NewButton("删除", func() {
			if err := u.core.DeleteSavedQuery(saved.ID); err != nil {
				u.showError(err)
				return
			}
			favoriteList.Remove(open)
			if d != nil {
				d.Hide()
			}
			u.setStatus("已删除收藏 %s", saved.Name)
		})
		favoriteList.Add(container.NewGridWithColumns(2, open, remove))
	}
	if len(favorites) == 0 {
		favoriteList.Add(widget.NewLabel("暂无收藏，可在查询页点击「保存收藏」"))
	}

	historyList := container.NewVBox()
	for _, item := range history {
		entry := item
		label := fmt.Sprintf("%s  %s  %s  %s",
			entry.At.Local().Format("01-02 15:04"), entry.Connection,
			entry.Action, shortText(entry.Text, 60))
		historyList.Add(widget.NewButton(label, func() {
			pick(queryPick{
				Text: entry.Text, Connection: entry.Connection, Database: entry.Database,
				Collection: entry.Collection, Language: entry.Language,
			})
		}))
	}
	if len(history) == 0 {
		historyList.Add(widget.NewLabel("暂无查询历史"))
	}
	clear := widget.NewButton("清除历史", func() {
		if err := u.core.ClearHistory(); err != nil {
			u.showError(err)
			return
		}
		if d != nil {
			d.Hide()
		}
		u.setStatus("查询历史已清空")
	})

	tabs := container.NewAppTabs(
		container.NewTabItem("收藏", container.NewVScroll(favoriteList)),
		container.NewTabItem("历史", container.NewBorder(clear, nil, nil, nil,
			container.NewVScroll(historyList))),
	)
	wrapped := container.NewGridWrap(fyne.NewSize(720, 420), tabs)
	d = dialog.NewCustom("查询历史与收藏", "关闭", wrapped, u.win)
	d.Resize(fyne.NewSize(760, 500))
	d.Show()
}

func shortText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}
