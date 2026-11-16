//go:build custom_providers

// Package all 在 custom_providers 模式下只装配通过 db_* 构建标签显式选择的数据库模块。
// 各 db_*.go 文件与 all.go（默认全量）互斥，由构建标签决定启用哪些模块。
package all
