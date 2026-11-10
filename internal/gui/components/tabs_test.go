//go:build windows && cgo

package components

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func collectTexts(objs []fyne.CanvasObject, out *[]string) {
	for _, o := range objs {
		switch v := o.(type) {
		case *widget.Label:
			*out = append(*out, v.Text)
		case fyne.Widget:
			if r := test.WidgetRenderer(v); r != nil {
				collectTexts(r.Objects(), out)
			}
		case *fyne.Container:
			collectTexts(v.Objects, out)
		}
	}
}

func TestTabStripRendersTitles(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	s := NewTabStrip()
	view := s.Build()
	_ = view
	s.Add(&TabItem{ID: "t1", Title: "用户表 @ app", Content: widget.NewLabel("x"), Closable: true})
	s.Add(&TabItem{ID: "t2", Title: "订单表 @ shop", Content: widget.NewLabel("y"), Closable: true})

	var texts []string
	collectTexts(s.barBox.Objects, &texts)
	joined := strings.Join(texts, " | ")
	if !containsStr(texts, "用户表 @ app") {
		t.Fatalf("tab 标题未渲染，已渲染文本: %q", joined)
	}
	if !containsStr(texts, "订单表 @ shop") {
		t.Fatalf("tab 标题未渲染，已渲染文本: %q", joined)
	}

	// 验证芯片在真实窗口中确实获得非零尺寸，且标题未被截断为「…」。
	win := test.NewWindow(s.view)
	defer win.Close()
	win.Resize(fyne.NewSize(900, 200))
	if len(s.barBox.Objects) == 0 {
		t.Fatal("标题栏没有任何芯片")
	}
	for _, obj := range s.barBox.Objects {
		chip, ok := obj.(*tabChip)
		if !ok {
			t.Fatalf("标题栏对象不是 tabChip，而是 %T", obj)
		}
		if chip.Size().Width <= 0 || chip.Size().Height <= 0 {
			t.Fatalf("芯片尺寸为零，标题将被裁剪: %v", chip.Size())
		}
		r, ok := test.WidgetRenderer(chip).(*tabChipRenderer)
		if !ok {
			t.Fatalf("芯片渲染器类型异常: %T", test.WidgetRenderer(chip))
		}
		if r.label.Size().Width+1 < r.label.MinSize().Width {
			t.Fatalf("标题被截断: 可用 %.0f < 需要 %.0f (%q)",
				r.label.Size().Width, r.label.MinSize().Width, r.label.Text)
		}
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
