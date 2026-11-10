//go:build windows && cgo

package components

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// TabItem 是多文档工作区中的一个页签。ID 用于去重（例如每张表只开一个 tab）。
type TabItem struct {
	ID       string
	Title    string
	Content  fyne.CanvasObject
	Closable bool
	OnClose  func()
}

// TabStrip 是可关闭的多文档标签容器。Fyne 的 AppTabs 不支持标题栏关闭按钮，
// 因此这里用自定义 tabChip + Stack 内容区实现，供所有数据库类别共用。
type TabStrip struct {
	mu       sync.Mutex
	items    []*TabItem
	activeID string
	chips    map[string]*tabChip
	onChange func(*TabItem)

	barBox    *fyne.Container
	barScroll *container.Scroll
	body      *fyne.Container
	view      fyne.CanvasObject
	built     bool
}

func NewTabStrip() *TabStrip {
	return &TabStrip{chips: make(map[string]*tabChip)}
}

func (s *TabStrip) Build() fyne.CanvasObject {
	s.barBox = container.NewHBox()
	s.barScroll = container.NewHScroll(s.barBox)
	s.barScroll.SetMinSize(fyne.NewSize(0, 44))
	s.body = container.NewStack()
	s.view = container.NewBorder(s.barScroll, nil, nil, nil, s.body)
	s.built = true
	s.rebuild()
	return s.view
}

func (s *TabStrip) SetOnChanged(fn func(*TabItem)) { s.onChange = fn }

func (s *TabStrip) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *TabStrip) Items() []*TabItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*TabItem(nil), s.items...)
}

func (s *TabStrip) Find(id string) *TabItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

func (s *TabStrip) Active() *TabItem { return s.Find(s.activeID) }

// Add 加入新页签；若 ID 已存在则直接激活，保证「每张表只开一个 tab」。
func (s *TabStrip) Add(item *TabItem) {
	if item == nil || item.ID == "" {
		return
	}
	s.mu.Lock()
	for _, existing := range s.items {
		if existing.ID == item.ID {
			s.mu.Unlock()
			s.Select(item.ID)
			return
		}
	}
	s.items = append(s.items, item)
	if s.activeID == "" {
		s.activeID = item.ID
	}
	s.mu.Unlock()
	s.rebuild()
	s.Select(item.ID)
}

func (s *TabStrip) Select(id string) {
	s.mu.Lock()
	found := false
	for _, item := range s.items {
		if item.ID == id {
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return
	}
	s.activeID = id
	items := append([]*TabItem(nil), s.items...)
	active := s.activeID
	s.mu.Unlock()

	s.sync(active, items)
	current := s.Find(active)
	if s.onChange != nil {
		s.onChange(current)
	}
}

func (s *TabStrip) Remove(id string) {
	s.mu.Lock()
	index := -1
	for i, item := range s.items {
		if item.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return
	}
	item := s.items[index]
	s.items = append(s.items[:index], s.items[index+1:]...)
	delete(s.chips, id)
	next := ""
	if len(s.items) > 0 {
		if id == s.activeID {
			next = s.items[min(index, len(s.items)-1)].ID
		} else {
			next = s.activeID
		}
	}
	s.activeID = next
	s.mu.Unlock()

	if item.OnClose != nil {
		item.OnClose()
	}
	if s.built {
		s.body.Remove(item.Content)
	}
	s.rebuild()
	if next != "" {
		s.Select(next)
	}
}

// rebuild 重建标题栏并把所有内容对象放入 Stack（隐藏非活动项）。
func (s *TabStrip) rebuild() {
	if !s.built {
		return
	}
	s.mu.Lock()
	items := append([]*TabItem(nil), s.items...)
	active := s.activeID
	s.mu.Unlock()

	s.barBox.RemoveAll()
	s.mu.Lock()
	s.chips = make(map[string]*tabChip, len(items))
	s.mu.Unlock()
	for _, item := range items {
		id := item.ID
		chip := newTabChip(item.Title, item.Closable,
			func() { s.Select(id) },
			func() { s.Remove(id) })
		chip.SetActive(id == active)
		s.mu.Lock()
		s.chips[id] = chip
		s.mu.Unlock()
		s.barBox.Add(chip)
	}
	for _, item := range items {
		if item.Content != nil && !containerHas(s.body, item.Content) {
			s.body.Add(item.Content)
		}
	}
	s.sync(active, items)
	s.barBox.Refresh()
	s.barScroll.Refresh()
}

func (s *TabStrip) sync(active string, items []*TabItem) {
	if !s.built {
		return
	}
	for _, item := range items {
		if item.Content == nil {
			continue
		}
		if item.ID == active {
			item.Content.Show()
		} else {
			item.Content.Hide()
		}
	}
	s.mu.Lock()
	chips := make(map[string]*tabChip, len(s.chips))
	for id, chip := range s.chips {
		chips[id] = chip
	}
	s.mu.Unlock()
	for id, chip := range chips {
		chip.SetActive(id == active)
	}
	s.body.Refresh()
}

func containerHas(c *fyne.Container, obj fyne.CanvasObject) bool {
	for _, child := range c.Objects {
		if child == obj {
			return true
		}
	}
	return false
}

// tabChip 是单个页签芯片。使用显式渲染器（背景 + 标题 + 关闭按钮）而非嵌套容器，
// 确保标题始终按可用宽度布局，不会被压成「…」而看不见名称。
type tabChip struct {
	widget.BaseWidget
	title    string
	closable bool
	active   bool
	onSelect func()
	onClose  func()
}

var _ fyne.Tappable = (*tabChip)(nil)

func newTabChip(title string, closable bool, onSelect, onClose func()) *tabChip {
	c := &tabChip{title: title, closable: closable, onSelect: onSelect, onClose: onClose}
	c.ExtendBaseWidget(c)
	return c
}

func (c *tabChip) Tapped(*fyne.PointEvent) {
	if c.onSelect != nil {
		c.onSelect()
	}
}

func (c *tabChip) SetActive(active bool) {
	if c.active == active {
		return
	}
	c.active = active
	c.Refresh()
}

// tabChipRenderer 显式绘制芯片：底层背景矩形、左侧标题、右侧关闭按钮。
type tabChipRenderer struct {
	chip  *tabChip
	bg    *canvas.Rectangle
	label *widget.Label
	close *widget.Button
	objs  []fyne.CanvasObject
}

func (c *tabChip) CreateRenderer() fyne.WidgetRenderer {
	r := &tabChipRenderer{chip: c}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.label = widget.NewLabel(c.title)
	r.label.Truncation = fyne.TextTruncateEllipsis
	r.label.Wrapping = fyne.TextWrapOff
	if c.closable {
		r.close = widget.NewButton("×", func() {
			if c.onClose != nil {
				c.onClose()
			}
		})
		r.close.Importance = widget.LowImportance
		r.objs = []fyne.CanvasObject{r.bg, r.label, r.close}
	} else {
		r.objs = []fyne.CanvasObject{r.bg, r.label}
	}
	r.Refresh()
	return r
}

func (r *tabChipRenderer) Layout(size fyne.Size) {
	pad := theme.InnerPadding()
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	rightEdge := size.Width - pad
	if r.close != nil {
		cw := r.close.MinSize().Width
		r.close.Resize(fyne.NewSize(cw, size.Height))
		r.close.Move(fyne.NewPos(size.Width-cw, 0))
		rightEdge = size.Width - cw - pad
	}
	lw := rightEdge - pad
	if lw < 0 {
		lw = 0
	}
	lh := r.label.MinSize().Height
	if lh > size.Height {
		lh = size.Height
	}
	r.label.Resize(fyne.NewSize(lw, lh))
	r.label.Move(fyne.NewPos(pad, (size.Height-lh)/2))
}

func (r *tabChipRenderer) MinSize() fyne.Size {
	pad := theme.InnerPadding()
	w := r.label.MinSize().Width + pad*3
	if r.close != nil {
		if cw := r.close.MinSize().Width; cw > 0 {
			w += cw
		}
	}
	// 过窄的芯片会让名称被截断，过宽又占地方：夹在 [128, 300] 之间。
	if w < 128 {
		w = 128
	}
	if w > 300 {
		w = 300
	}
	h := r.label.MinSize().Height + pad*2
	if h < 36 {
		h = 36
	}
	return fyne.NewSize(w, h)
}

func (r *tabChipRenderer) Refresh() {
	if r.chip.active {
		r.bg.FillColor = chipColor(theme.ColorNameSelection)
		r.label.TextStyle = fyne.TextStyle{Bold: true}
	} else {
		r.bg.FillColor = color.Transparent
		r.label.TextStyle = fyne.TextStyle{}
	}
	r.label.SetText(r.chip.title)
	r.bg.Refresh()
}

func (r *tabChipRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *tabChipRenderer) Destroy()                     {}

func chipColor(name fyne.ThemeColorName) color.Color {
	settings := fyne.CurrentApp().Settings()
	return settings.Theme().Color(name, settings.ThemeVariant())
}
