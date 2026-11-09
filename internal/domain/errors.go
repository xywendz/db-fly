package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrorCode 是跨层统一的领域错误码，表现层只认这些码。
type ErrorCode string

const (
	CodeValidation      ErrorCode = "validation"
	CodeNotFound        ErrorCode = "not_found"
	CodeAuthFailed      ErrorCode = "auth_failed"
	CodeConnection      ErrorCode = "connection"
	CodeQuery           ErrorCode = "query"
	CodeSchema          ErrorCode = "schema"
	CodeUnsupported     ErrorCode = "unsupported"
	CodeNotImplemented  ErrorCode = "not_implemented"
	CodeCanceled        ErrorCode = "canceled"
	CodeTimeout         ErrorCode = "timeout"
	CodeReadOnly        ErrorCode = "read_only"
	CodePermission      ErrorCode = "permission"
	CodeProviderMissing ErrorCode = "provider_missing"
)

// Error 是携带错误码、操作名与原因链的领域错误。
type Error struct {
	Code    ErrorCode
	Op      string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	if e.Cause != nil {
		b.WriteString(" (")
		b.WriteString(e.Cause.Error())
		b.WriteString(")")
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError 构造一个不带底层原因的领域错误。
func NewError(code ErrorCode, op, format string, args ...any) *Error {
	return &Error{Code: code, Op: op, Message: fmt.Sprintf(format, args...)}
}

// WrapError 把驱动/系统错误翻译成领域错误。
func WrapError(code ErrorCode, op string, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Op: op, Message: fmt.Sprintf(format, args...), Cause: cause}
}

// CodeOf 提取错误链中的领域错误码。
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		return de.Code
	}
	if errors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimeout
	}
	return CodeQuery
}

// IsCode 判断错误链中是否存在指定错误码。
func IsCode(err error, code ErrorCode) bool {
	var de *Error
	if errors.As(err, &de) && de.Code == code {
		return true
	}
	return CodeOf(err) == code
}

// UserMessage 把领域错误转成可直接展示的友好文案。
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		switch de.Code {
		case CodeAuthFailed:
			return "认证失败：请检查用户名、密码或认证方式。"
		case CodeConnection:
			return "连接失败：请检查主机、端口、网络与防火墙设置。"
		case CodeCanceled:
			return "操作已取消。"
		case CodeTimeout:
			return "操作超时，可调整连接超时或缩小查询范围。"
		case CodeReadOnly:
			return "当前连接处于只读模式，禁止执行该操作。"
		case CodeUnsupported:
			return "当前数据库不支持此操作。"
		case CodeNotImplemented:
			return de.Message
		case CodeProviderMissing:
			return "该数据库类型的业务模块尚未注册。"
		default:
			return de.Error()
		}
	}
	return err.Error()
}
