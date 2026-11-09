//go:build windows && cgo

package gui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// jsonTreeModel 把原始 BSON/JSON 文档展开为 widget.Tree 所需的父子关系。
// UID 使用「父路径/子节点序号」，避免文档字段名中的斜杠造成冲突。
type jsonTreeModel struct {
	children map[widget.TreeNodeID][]widget.TreeNodeID
	labels   map[widget.TreeNodeID]string
}

func newJSONTree(documents []string) *widget.Tree {
	model := &jsonTreeModel{
		children: make(map[widget.TreeNodeID][]widget.TreeNodeID),
		labels:   make(map[widget.TreeNodeID]string),
	}
	root := make([]widget.TreeNodeID, 0, len(documents))
	for i, doc := range documents {
		id := widget.TreeNodeID(fmt.Sprintf("doc:%d", i))
		model.labels[id] = fmt.Sprintf("文档 %d", i+1)
		root = append(root, id)
		var value any
		if err := json.Unmarshal([]byte(doc), &value); err != nil {
			model.labels[id] += "（解析失败: " + err.Error() + "）"
			continue
		}
		model.add(id, value)
	}
	model.children[""] = root

	return widget.NewTree(
		func(uid widget.TreeNodeID) []widget.TreeNodeID { return model.children[uid] },
		func(uid widget.TreeNodeID) bool { return len(model.children[uid]) > 0 },
		func(bool) fyne.CanvasObject { return widget.NewLabel("") },
		func(uid widget.TreeNodeID, _ bool, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(model.labels[uid])
		},
	)
}

func (m *jsonTreeModel) add(parent widget.TreeNodeID, value any) {
	appendChild := func(label string, child any) {
		id := parent + "/" + widget.TreeNodeID(strconv.Itoa(len(m.children[parent])))
		m.children[parent] = append(m.children[parent], id)
		switch typed := child.(type) {
		case map[string]any:
			m.labels[id] = label + " {…}"
			m.add(id, typed)
		case []any:
			m.labels[id] = fmt.Sprintf("%s [%d]", label, len(typed))
			m.add(id, typed)
		default:
			m.labels[id] = label + ": " + jsonScalar(child)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			appendChild(key, typed[key])
		}
	case []any:
		for i, item := range typed {
			appendChild("["+strconv.Itoa(i)+"]", item)
		}
	default:
		m.labels[parent] += ": " + jsonScalar(value)
	}
}

func jsonScalar(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
