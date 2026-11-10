//go:build windows && cgo

package components

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// EditableCell 是结果网格的单元格模板：普通状态显示 Label，编辑状态显示 Entry。
// Fyne 的 widget.Table 会吞掉单元格点击（Table.Tapped），因此模板自身实现
// fyne.Tappable 以接管单击，把编辑框直接放进原单元格。
type EditableCell struct {
	widget.BaseWidget
	Label *widget.Label
	Entry *widget.Entry

	ID       widget.TableCellID
	OnTap    func(widget.TableCellID)
	OnSubmit func(widget.TableCellID, string)

	box          *fyne.Container
	editingShown bool
}

var _ fyne.Tappable = (*EditableCell)(nil)

func NewEditableCell() *EditableCell {
	c := &EditableCell{}
	c.Label = NewCellLabel()
	c.Entry = widget.NewEntry()
	c.Entry.OnSubmitted = func(text string) {
		if c.OnSubmit != nil {
			c.OnSubmit(c.ID, text)
		}
	}
	c.box = container.NewStack(c.Label, c.Entry)
	c.Entry.Hide()
	c.ExtendBaseWidget(c)
	return c
}

func (c *EditableCell) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.box)
}

func (c *EditableCell) Tapped(*fyne.PointEvent) {
	if c.OnTap != nil {
		c.OnTap(c.ID)
	}
}

// ShowEditor 只在首次切入编辑态时写入初始值，避免刷新覆盖用户正在输入的内容。
func (c *EditableCell) ShowEditor(initial string) {
	if !c.editingShown {
		c.Entry.SetText(initial)
		c.editingShown = true
	}
	c.Label.Hide()
	c.Entry.Show()
	c.box.Refresh()
}

func (c *EditableCell) ShowLabel(text string) {
	if c.editingShown {
		c.editingShown = false
		c.Entry.Hide()
		c.Label.Show()
		c.box.Refresh()
	}
	// 表头需要显示「列名 + 类型」两行，不能用省略号截断；数据行保留截断避免重叠。
	truncation := fyne.TextTruncateEllipsis
	if c.ID.Row == 0 {
		truncation = fyne.TextTruncateOff
	}
	if c.Label.Truncation != truncation {
		c.Label.Truncation = truncation
		c.Label.Refresh()
	}
	if c.Label.Text != text {
		c.Label.SetText(text)
	}
}

func (c *EditableCell) Focus(canvas fyne.Canvas) {
	if canvas != nil {
		canvas.Focus(c.Entry)
		c.Entry.CursorColumn = len([]rune(c.Entry.Text))
		c.Entry.Refresh()
	}
}
