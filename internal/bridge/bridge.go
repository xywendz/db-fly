// Package bridge 把业务内核（internal/app）以 wails 服务的形式暴露给 Vue 前端。
//
// 设计原则：
//   - 本包不依赖 wails/WebView，保持纯 Go，便于在无 CGO 环境下编译校验；
//     仅 cmd/db-fly-gui 的入口文件引入 wails 并负责把 Bridge 注册为服务、注入事件发射器。
//   - 所有方法签名只使用 JSON 可序列化的参数与返回值，首参不暴露 Go 的 context.Context，
//     由方法内部构造 context；错误统一以 (T, error) 形式返回，由 wails 转成 Promise 的 reject。
//   - 按业务模块分文件组织方法：连接 / 元数据 / 查询 / 结构 / 数据 / 导入 / 历史收藏。
package bridge

import (
	"context"
	"time"

	"github.com/db-fly/db-fly/internal/app"
	"github.com/db-fly/db-fly/internal/domain"
)

// EventEmitter 把后端事件（进度、通知）推送给前端。
type EventEmitter interface {
	Emit(name string, data ...any)
}

// Version 由构建脚本经 -ldflags "-X .../internal/bridge.Version=..." 注入。
var Version = "dev"

// 事件发射器以包级变量注入（由 wails 入口在创建应用后调用 SetDefaultEmitter）。
// 使用包级变量而非 Bridge 的导出方法，是为了避免把不可序列化的注入接口暴露给前端绑定生成器。
var defaultEmitter EventEmitter

// SetDefaultEmitter 设置全局事件发射器；必须在应用启动前调用一次。
func SetDefaultEmitter(e EventEmitter) { defaultEmitter = e }

// Bridge 聚合全部业务服务，是 wails 暴露给前端唯一的服务实例。
// 其构造与运行时信息注入均通过包内函数完成，故这些方法保持未导出，避免被绑定生成器暴露到前端。
type Bridge struct {
	app *app.App

	version     string
	dataDir     string
	secretsName string
}

// newBridge 创建 Bridge，运行时信息由 setRuntimeInfo 注入。
func newBridge(a *app.App) *Bridge {
	return &Bridge{app: a, version: Version}
}

// setRuntimeInfo 设置运行时信息（版本、数据目录、凭证实现名）。
func (b *Bridge) setRuntimeInfo(version, dataDir, secretsName string) {
	if version != "" {
		b.version = version
	}
	if dataDir != "" {
		b.dataDir = dataDir
	}
	if secretsName != "" {
		b.secretsName = secretsName
	}
}

func (b *Bridge) emit(name string, data ...any) {
	if defaultEmitter != nil {
		defaultEmitter.Emit(name, data...)
	}
}

// ctx 构造一个后台 context。业务内核会在 prepareQuery 内按连接配置施加超时。
func (b *Bridge) ctx() context.Context { return context.Background() }

// withTimeout 构造带超时的 context（秒为 0 表示不额外限制）。
func (b *Bridge) withTimeout(sec int) (context.Context, context.CancelFunc) {
	if sec <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), time.Duration(sec)*time.Second)
}

// KindInfo 描述一种已注册数据库类型的元信息，供前端动态渲染连接表单与菜单。
type KindInfo struct {
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	DefaultPort  int      `json:"default_port"`
	Relational   bool     `json:"relational"`
	FileBased    bool     `json:"file_based"`
	Capabilities []string `json:"capabilities"`
	Languages    []string `json:"languages"`
}

func labelOf(k domain.Kind) string {
	switch k {
	case domain.KindMySQL:
		return "MySQL"
	case domain.KindSQLServer:
		return "SQL Server"
	case domain.KindMongoDB:
		return "MongoDB"
	case domain.KindPostgreSQL:
		return "PostgreSQL"
	case domain.KindOracle:
		return "Oracle"
	case domain.KindSQLite:
		return "SQLite"
	case domain.KindRedis:
		return "Redis"
	default:
		return string(k)
	}
}

func languagesOf(caps domain.CapabilitySet) []string {
	var langs []string
	if caps.Has(domain.CapSQL) {
		langs = append(langs, string(domain.LangSQL))
	}
	if caps.Has(domain.CapMongoFilter) {
		langs = append(langs, string(domain.LangMongoFilter), string(domain.LangMongoPipeline))
	}
	if caps.Has(domain.CapKeyValues) {
		langs = append(langs, string(domain.LangRedis))
	}
	return langs
}

// DomainError 是面向前端的错误结构，便于 UI 展示错误码与可读消息。
type DomainError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorToDTO 把任意错误整理成前端可消费的 JSON 结构。
// wails 已会把 Go error 转成 reject 的 message，这里额外提供结构化信息供需要细分处理的场景。
func errorToDTO(err error) DomainError {
	if err == nil {
		return DomainError{}
	}
	if de, ok := err.(*domain.Error); ok {
		return DomainError{Code: string(de.Code), Message: domain.UserMessage(err)}
	}
	return DomainError{Code: "error", Message: err.Error()}
}
