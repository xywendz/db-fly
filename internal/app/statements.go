package app

import (
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

type statementSpan struct {
	Text       string
	Start, End int // rune 下标，End 包含语句后的分号
}

// SplitStatements 按分号拆分 SQL 文本，忽略字符串、标识符与注释中的分号。
// 供 CLI 批量执行与 GUI「仅执行当前语句」使用。
func SplitStatements(text string) []string {
	spans := splitWithPos(text)
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Text)
	}
	return out
}

func splitWithPos(text string) []statementSpan {
	var (
		out        []statementSpan
		buf        strings.Builder
		segStart   int
		inSingle   bool
		inDouble   bool
		inBacktick bool
		inLineCmt  bool
		inBlockCmt bool
	)
	runes := []rune(text)

	emit := func(end int) {
		if s := strings.TrimSpace(buf.String()); s != "" {
			out = append(out, statementSpan{Text: s, Start: segStart, End: end})
		}
		buf.Reset()
	}

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		if inLineCmt {
			if ch == '\n' {
				inLineCmt = false
				buf.WriteRune(ch)
			}
			continue
		}
		if inBlockCmt {
			if ch == '*' && next == '/' {
				inBlockCmt = false
				i++
			}
			continue
		}
		if inSingle || inDouble || inBacktick {
			buf.WriteRune(ch)
			if ch == '\\' && i+1 < len(runes) && (inSingle || inDouble) {
				i++
				buf.WriteRune(runes[i])
				continue
			}
			if (inSingle && ch == '\'') || (inDouble && ch == '"') || (inBacktick && ch == '`') {
				if i+1 < len(runes) && runes[i+1] == ch {
					i++
					buf.WriteRune(runes[i])
					continue
				}
				inSingle, inDouble, inBacktick = false, false, false
			}
			continue
		}

		switch {
		case ch == '-' && next == '-':
			inLineCmt = true
			i++
		case ch == '#':
			inLineCmt = true
		case ch == '/' && next == '*':
			inBlockCmt = true
			i++
		case ch == '\'':
			inSingle = true
			buf.WriteRune(ch)
		case ch == '"':
			inDouble = true
			buf.WriteRune(ch)
		case ch == '`':
			inBacktick = true
			buf.WriteRune(ch)
		case ch == ';':
			emit(i)
			segStart = i + 1
		default:
			buf.WriteRune(ch)
		}
	}
	emit(len(runes) - 1)
	return out
}

// StatementAt 返回光标位置（rune 下标）所在的完整语句。
// 光标位于分号之后时返回下一条语句；位于末尾空白时返回最后一条。
func StatementAt(text string, pos int) string {
	runes := []rune(text)
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	spans := splitWithPos(text)
	if len(spans) == 0 {
		return ""
	}
	for _, s := range spans {
		if pos <= s.End {
			return s.Text
		}
	}
	return spans[len(spans)-1].Text
}

var readOnlyPrefixes = []string{
	"select", "show", "describe", "desc", "explain", "with", "use", "set",
	"pragma", "help", "analyze", "check", "table", "status", "variables",
}

// CheckReadOnly 在只读模式下拒绝写操作。
// 这是辅助保护而非安全边界，真正的写权限应由数据库账号控制。
func CheckReadOnly(sqlText string) error {
	trimmed := strings.TrimSpace(sqlText)
	for strings.HasPrefix(trimmed, "/*") {
		if end := strings.Index(trimmed, "*/"); end >= 0 {
			trimmed = strings.TrimSpace(trimmed[end+2:])
		} else {
			break
		}
	}
	if trimmed == "" {
		return nil
	}
	fields := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '('
	})
	if len(fields) == 0 {
		return nil
	}
	word := strings.ToLower(fields[0])
	for _, p := range readOnlyPrefixes {
		if word == p {
			return nil
		}
	}
	return domain.NewError(domain.CodeReadOnly, "query", "只读连接禁止执行 %q 语句", word)
}
