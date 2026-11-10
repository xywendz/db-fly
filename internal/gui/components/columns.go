//go:build windows && cgo

package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

// SchemaColumnHeaders 是表结构页「字段」网格的统一列序：
// 字段 / 类型 / 长度 / 小数位 / 非空 / 虚拟 / 键 / 注释。
var SchemaColumnHeaders = []string{"字段", "类型", "长度", "小数位", "非空", "虚拟", "键", "注释"}

// ColumnTypeParts 把 varchar(32)、decimal(10,2) unsigned 之类的原始类型
// 拆成参考图里的 类型 / 长度 / 小数位 / 无符号 / 补零 几栏。
// 这些是纯展示派生信息，真正的字段定义仍以 RawType 原文回写脚本。
type ColumnTypeParts struct {
	Base     string
	Length   string
	Decimals string
	Unsigned bool
	Zerofill bool
}

func ParseColumnType(raw string, length int64) ColumnTypeParts {
	text := strings.ToLower(strings.TrimSpace(raw))
	parts := ColumnTypeParts{
		Unsigned: strings.Contains(text, "unsigned"),
		Zerofill: strings.Contains(text, "zerofill"),
	}
	base := text
	if open := strings.IndexByte(text, '('); open >= 0 {
		base = text[:open]
		if close := strings.IndexByte(text[open:], ')'); close >= 0 {
			args := text[open+1 : open+close]
			fields := strings.SplitN(args, ",", 2)
			parts.Length = strings.TrimSpace(fields[0])
			if len(fields) > 1 {
				parts.Decimals = strings.TrimSpace(fields[1])
			}
		}
	}
	for _, keyword := range []string{"unsigned", "zerofill"} {
		base = strings.ReplaceAll(base, keyword, "")
	}
	parts.Base = strings.TrimSpace(base)
	if parts.Length == "" && length > 0 {
		parts.Length = strconv.FormatInt(length, 10)
	}
	return parts
}

func ColumnRawType(c domain.ColumnInfo) string {
	if c.RawType != "" {
		return c.RawType
	}
	return c.DataType
}

// SchemaColumnCell 生成字段网格第 col 列的文本。
func SchemaColumnCell(c domain.ColumnInfo, col int) string {
	parts := ParseColumnType(ColumnRawType(c), c.Length)
	switch col {
	case 0:
		return c.Name
	case 1:
		return parts.Base
	case 2:
		return parts.Length
	case 3:
		return parts.Decimals
	case 4:
		return BoolText(!c.Nullable)
	case 5:
		return "" // 虚拟列：现有模块未暴露该元数据
	case 6:
		if c.PrimaryKey {
			return "PK 1"
		}
		return ""
	case 7:
		return c.Comment
	}
	return ""
}

// ColumnSpecToInfo 让「新建表」设计器复用与表结构页相同的网格渲染。
func ColumnSpecToInfo(c domain.ColumnSpec) domain.ColumnInfo {
	info := domain.ColumnInfo{
		Name: c.Name, RawType: c.Type, DataType: c.Type,
		Nullable: c.Nullable, PrimaryKey: c.PrimaryKey,
		AutoIncrement: c.AutoIncrement, Comment: c.Comment,
	}
	if c.Default != nil {
		info.HasDefault = true
		info.Default = *c.Default
	}
	return info
}

// BuildSchemaComment 汇总表注释与字段注释，供「注释」页签只读展示。
func BuildSchemaComment(schema domain.TableSchema) string {
	var b strings.Builder
	if schema.Comment != "" {
		b.WriteString("表注释: " + schema.Comment + "\n")
	} else {
		b.WriteString("表注释: (空)\n")
	}
	b.WriteString("\n字段注释:\n")
	if len(schema.Columns) == 0 {
		b.WriteString("  (无字段元数据)\n")
	}
	for _, c := range schema.Columns {
		fmt.Fprintf(&b, "  %-28s %s\n", c.Name, c.Comment)
	}
	return b.String()
}
