package domain

import "context"

// Provider 是每个数据库业务模块的唯一入口。
// 各模块通过 init() 自注册，彼此之间禁止互相 import。
type Provider interface {
	Kind() Kind
	Capabilities() CapabilitySet
	Open(ctx context.Context, cfg ConnectionConfig) (Session, error)
}

// Session 代表一个已建立的连接会话，持有独立的连接池与生命周期。
type Session interface {
	Kind() Kind
	Capabilities() CapabilitySet
	ServerInfo(ctx context.Context) (ServerInfo, error)
	Metadata() MetadataBrowser
	Schema() SchemaEditor
	Query() QueryExecutor
	Data() DataEditor
	// Maintenance 返回表数据清空操作脚本生成器；不支持的模块返回 nil。
	Maintenance() TableMaintenance
	Close() error
}

// TableMaintenance 为 TRUNCATE / DELETE FROM 等危险操作生成「可预览 → 显式确认 → 执行」的脚本。
// 它是可选端口：只有声明 CapTruncateTable / CapEmptyTable 的关系型模块需要实现。
type TableMaintenance interface {
	TableActionScript(ctx context.Context, ref ObjectRef, action TableAction) (Script, error)
}

// MetadataBrowser 提供元数据树所需的只读查询能力。
type MetadataBrowser interface {
	ListDatabases(ctx context.Context) ([]DatabaseInfo, error)
	ListSchemas(ctx context.Context, database string) ([]string, error)
	ListObjects(ctx context.Context, database, schema string) ([]ObjectInfo, error)
	Describe(ctx context.Context, ref ObjectRef) (TableSchema, error)
}

// SchemaEditor 负责「结构化编辑 → 方言脚本 → 预览 → 执行」全流程。
// 表现层永远不直接拼接 DDL。
type SchemaEditor interface {
	CreateDatabaseScript(ctx context.Context, spec DatabaseSpec) (Script, error)
	AlterDatabaseScript(ctx context.Context, name string, spec DatabaseSpec) (Script, error)
	CreateTableScript(ctx context.Context, def TableDefinition) (Script, error)
	AlterTableScript(ctx context.Context, ref ObjectRef, changes []SchemaChange) (Script, error)
	DropObjectScript(ctx context.Context, ref ObjectRef) (Script, error)
	ShowCreate(ctx context.Context, ref ObjectRef) (string, error)
	Apply(ctx context.Context, script Script) (ApplyResult, error)
}

// QueryExecutor 负责执行查询与无结果集语句。
type QueryExecutor interface {
	Execute(ctx context.Context, q Query) (ResultSet, error)
	// Exec 的 database 参数用于把语句绑定在指定库的同一条连接上执行。
	Exec(ctx context.Context, statement, database string) (ExecResult, error)
	Explain(ctx context.Context, q Query) (ResultSet, error)
}

// ResultSet 是流式结果集。调用方应在 Close 之前完整消费或主动取消 context。
type ResultSet interface {
	Columns() []Column
	Next() bool
	Row() Row
	// Document 返回当前行的原始 JSON 文档；关系型实现返回空串。
	Document() string
	Err() error
	// NextResultSet 切换到下一个结果集，切换后 Columns/Next 返回新结果集内容。
	NextResultSet() bool
	Affected() int64
	Messages() []ServerMessage
	Close() error
}

// DataEditor 负责结果集数据编辑回写，由各模块生成参数化语句。
type DataEditor interface {
	// Preview 返回将要执行的语句/命令文本，不修改数据。
	Preview(ctx context.Context, req EditRequest) ([]string, error)
	// Apply 执行编辑；Transaction 为 true 时尽量使用事务（模块自行决定是否支持）。
	Apply(ctx context.Context, req EditRequest) (EditResult, error)
}
