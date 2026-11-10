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

	core "github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
)

// keyDetailView 是键值型数据库专用的详情面板：展示键类型、TTL、值预览与等价创建命令。
// 关系型与文档型布局不包含此面板。
type keyDetailView struct {
	u    *ui
	view fyne.CanvasObject

	info      *widget.Label
	dbValue   *widget.Label
	keyValue  *widget.Label
	typeValue *widget.Label
	ttlValue  *widget.Label
	value     *widget.Entry
	command   *widget.Entry

	createBtn *widget.Button
	deleteBtn *widget.Button

	conn     string
	database string
	key      string
	category dbCategory
}

func newKeyDetailView(u *ui) *keyDetailView { return &keyDetailView{u: u} }

func (v *keyDetailView) build() fyne.CanvasObject {
	v.info = widget.NewLabel("请在左侧选择一个键")
	v.dbValue = widget.NewLabel("-")
	v.keyValue = widget.NewLabel("-")
	v.typeValue = widget.NewLabel("-")
	v.ttlValue = widget.NewLabel("-")
	details := widget.NewForm(
		widget.NewFormItem("逻辑库", v.dbValue),
		widget.NewFormItem("键名", v.keyValue),
		widget.NewFormItem("类型", v.typeValue),
		widget.NewFormItem("TTL", v.ttlValue),
	)

	v.value = widget.NewMultiLineEntry()
	v.value.Wrapping = fyne.TextWrapOff
	v.command = widget.NewMultiLineEntry()
	v.command.Wrapping = fyne.TextWrapOff

	content := container.NewVSplit(
		container.NewBorder(widget.NewLabel("值预览"), nil, nil, nil, v.value),
		container.NewBorder(widget.NewLabel("等价创建命令"), nil, nil, nil, v.command),
	)
	content.Offset = 0.62

	v.createBtn = widget.NewButton("新建键", func() { v.u.showCreateKey() })
	v.deleteBtn = widget.NewButton("删除键", v.deleteKey)
	toolbar := container.NewHBox(
		widget.NewButton("刷新", v.reload),
		widget.NewButton("浏览键", func() { v.u.openKeyBrowser(v.conn, v.database) }),
		v.createBtn,
		v.deleteBtn,
	)
	v.view = container.NewBorder(container.NewVBox(toolbar, details), v.info, nil, nil, content)
	return v.view
}

func (v *keyDetailView) setCategory(cat dbCategory, caps domain.CapabilitySet) {
	v.category = cat
	show := func(btn *widget.Button, visible bool) {
		if visible {
			btn.Show()
		} else {
			btn.Hide()
		}
	}
	show(v.createBtn, caps.Has(domain.CapCreateTable))
	show(v.deleteBtn, caps.Has(domain.CapDropObject))
}

func (v *keyDetailView) setTarget(conn, database, key string) {
	v.conn, v.database, v.key = conn, database, key
}

func (v *keyDetailView) reload() { v.load(v.conn, v.database, v.key) }

func (v *keyDetailView) load(conn, database, key string) {
	v.conn, v.database, v.key = conn, database, key
	if key == "" || conn == "" {
		v.info.SetText("请在左侧选择一个键")
		v.value.SetText("")
		v.command.SetText("")
		return
	}
	ref := domain.ObjectRef{Database: database, Name: key}
	v.info.SetText("正在读取键详情 …")
	v.u.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		schema, describeErr := v.u.core.Describe(ctx, conn, ref)
		sink := &core.CollectSink{}
		out, queryErr := v.u.core.Execute(ctx, conn, domain.Query{
			Ref: ref, Database: database, MaxRows: 200,
		}, sink)
		command, commandErr := v.u.core.ShowCreate(ctx, conn, ref)
		fyne.Do(func() {
			v.render(ref, schema, describeErr, out, queryErr, command, commandErr)
		})
	})
}

func (v *keyDetailView) render(ref domain.ObjectRef, schema domain.TableSchema,
	describeErr error, out domain.QueryOutcome, queryErr error, command string, commandErr error) {

	if v.key != ref.Name || v.database != ref.Database {
		return
	}
	v.dbValue.SetText(ref.Database)
	v.keyValue.SetText(ref.Name)
	typ, ttl := "-", "-"
	if describeErr == nil {
		parts := strings.SplitN(schema.Comment, " · ", 2)
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			typ = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			ttl = strings.TrimSpace(strings.TrimPrefix(parts[1], "TTL "))
		}
	}
	v.typeValue.SetText(typ)
	v.ttlValue.SetText(ttl)

	switch {
	case queryErr != nil:
		v.value.SetText("读取键值失败：" + domain.UserMessage(queryErr))
	case len(out.Pages) == 0 || len(out.Pages[0].Rows) == 0:
		v.value.SetText("(空)")
	default:
		var b strings.Builder
		for _, page := range out.Pages {
			for _, row := range page.Rows {
				parts := make([]string, 0, len(row))
				for i, cell := range row {
					name := fmt.Sprintf("列%d", i+1)
					if i < len(page.Columns) {
						name = page.Columns[i].Name
					}
					parts = append(parts, name+"="+cellText(cell.Display()))
				}
				b.WriteString(strings.Join(parts, " | "))
				b.WriteString("\n")
			}
		}
		v.value.SetText(b.String())
	}

	switch {
	case commandErr != nil:
		v.command.SetText("读取等价命令失败：" + domain.UserMessage(commandErr))
	case command != "":
		v.command.SetText(command)
	case describeErr == nil:
		v.command.SetText(schema.DDL)
	default:
		v.command.SetText("")
	}
	v.info.SetText(fmt.Sprintf("%s · 类型 %s · TTL %s · %d 行预览",
		ref.String(), typ, ttl, out.Summary.Rows))
}

func (v *keyDetailView) deleteKey() {
	if v.key == "" || v.conn == "" {
		v.u.showInfo("提示", "请先在左侧选择一个键")
		return
	}
	conn := v.conn
	ref := domain.ObjectRef{Database: v.database, Name: v.key}
	dialog.ShowConfirm("删除键", "即将生成删除键 "+ref.String()+" 的命令，数据不可恢复。是否继续？", func(ok bool) {
		if !ok {
			return
		}
		v.u.async(func() {
			script, err := v.u.core.PreviewDropObject(context.Background(), conn, ref)
			if err != nil {
				v.u.showError(err)
				return
			}
			fyne.Do(func() { v.u.showScriptDialogWith("DEL 预览", conn, script, false) })
		})
	}, v.u.win)
}
