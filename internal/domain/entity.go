// Package domain 定义业务实体、值对象与端口契约。
// 本包只允许依赖标准库，禁止出现任何数据库方言、驱动或 UI 相关内容。
package domain

import (
	"encoding/hex"
	"fmt"
	"time"
)

// Kind 表示一个数据库业务模块的类型。
type Kind string

const (
	KindMySQL     Kind = "mysql"
	KindSQLServer Kind = "sqlserver"
	KindMongoDB   Kind = "mongodb"
)

// AllKinds 返回产品计划支持的全部数据库类型。
func AllKinds() []Kind {
	return []Kind{KindMySQL, KindSQLServer, KindMongoDB}
}

func (k Kind) Valid() bool {
	switch k {
	case KindMySQL, KindSQLServer, KindMongoDB:
		return true
	default:
		return false
	}
}

func (k Kind) String() string { return string(k) }

// DefaultPort 返回各数据库的默认端口。
func DefaultPort(k Kind) int {
	switch k {
	case KindMySQL:
		return 3306
	case KindSQLServer:
		return 1433
	case KindMongoDB:
		return 27017
	default:
		return 0
	}
}

// Capability 描述一个业务模块对外声明的能力，表现层据此动态生成菜单。
// 通用层禁止使用 if Kind == ... 判断，只能查询 CapabilitySet。
type Capability string

const (
	CapCreateDatabase Capability = "create_database"
	CapAlterDatabase  Capability = "alter_database"
	CapCreateTable    Capability = "create_table"
	CapAlterTable     Capability = "alter_table"
	CapDropObject     Capability = "drop_object"
	CapIndexes        Capability = "indexes"
	CapForeignKeys    Capability = "foreign_keys"
	CapSchemas        Capability = "schemas"
	CapSQL            Capability = "sql"
	CapMongoFilter    Capability = "mongo_filter"
	CapJSONTree       Capability = "json_tree"
	CapExplain        Capability = "explain"
	CapMultiResult    Capability = "multi_result"
	CapTransactions   Capability = "transactions"
	CapDataEdit       Capability = "data_edit"
)

// CapabilitySet 是能力集合。
type CapabilitySet map[Capability]bool

func (s CapabilitySet) Has(c Capability) bool { return s != nil && s[c] }

func (s CapabilitySet) Clone() CapabilitySet {
	out := make(CapabilitySet, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// ConnectionConfig 是持久化的连接定义。
// Password 不写入配置文件，运行期由 CredentialStore 注入。
type ConnectionConfig struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Kind       Kind              `json:"kind"`
	Host       string            `json:"host"`
	Port       int               `json:"port"`
	User       string            `json:"user,omitempty"`
	Database   string            `json:"database,omitempty"`
	Params     map[string]string `json:"params,omitempty"`
	TLSMode    string            `json:"tls_mode,omitempty"`
	ReadOnly   bool              `json:"read_only,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"`
	Group      string            `json:"group,omitempty"`
	Color      string            `json:"color,omitempty"`

	Password string `json:"-"`
}

// SecretKey 返回该连接在凭证库中的键。
func (c ConnectionConfig) SecretKey() string { return "conn:" + c.ID }

// EffectiveTimeout 返回连接/语句的默认超时。
func (c ConnectionConfig) EffectiveTimeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return 15 * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}

// ServerInfo 描述服务端版本信息。
type ServerInfo struct {
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`
	Edition string `json:"edition,omitempty"`
	Host    string `json:"host,omitempty"`
}

// DatabaseInfo 描述一个库（Mongo 中语义为 database）。
type DatabaseInfo struct {
	Name      string `json:"name"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
	System    bool   `json:"system,omitempty"`
}

// ObjectType 描述可浏览对象的类别。
type ObjectType string

const (
	ObjectTable      ObjectType = "table"
	ObjectView       ObjectType = "view"
	ObjectCollection ObjectType = "collection"
)

// ObjectInfo 描述树形浏览器中的一个节点。
type ObjectInfo struct {
	Database string     `json:"database"`
	Schema   string     `json:"schema,omitempty"`
	Name     string     `json:"name"`
	Type     ObjectType `json:"type"`
	Comment  string     `json:"comment,omitempty"`
	Rows     int64      `json:"rows,omitempty"`
}

// ObjectRef 唯一定位一个对象。Schema 在 MySQL/Mongo 中为空或等同 Database。
type ObjectRef struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	Name     string `json:"name"`
}

func (r ObjectRef) String() string {
	if r.Schema != "" && r.Schema != r.Database {
		return r.Database + "." + r.Schema + "." + r.Name
	}
	if r.Database != "" {
		return r.Database + "." + r.Name
	}
	return r.Name
}

// ColumnInfo 描述表的一个字段（来自元数据查询）。
type ColumnInfo struct {
	Position      int    `json:"position"`
	Name          string `json:"name"`
	DataType      string `json:"data_type"`
	RawType       string `json:"raw_type,omitempty"`
	Length        int64  `json:"length,omitempty"`
	Nullable      bool   `json:"nullable"`
	Default       string `json:"default,omitempty"`
	HasDefault    bool   `json:"has_default,omitempty"`
	PrimaryKey    bool   `json:"primary_key,omitempty"`
	AutoIncrement bool   `json:"auto_increment,omitempty"`
	Comment       string `json:"comment,omitempty"`
}

// IndexInfo 描述一个索引。
type IndexInfo struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	Unique    bool     `json:"unique,omitempty"`
	Primary   bool     `json:"primary,omitempty"`
	IndexType string   `json:"index_type,omitempty"`
}

// ForeignKeyInfo 描述一个外键约束。
type ForeignKeyInfo struct {
	Name        string   `json:"name"`
	Columns     []string `json:"columns"`
	RefDatabase string   `json:"ref_database,omitempty"`
	RefTable    string   `json:"ref_table"`
	RefColumns  []string `json:"ref_columns"`
}

// TableSchema 是一个对象的完整结构定义。
type TableSchema struct {
	Ref         ObjectRef        `json:"ref"`
	Columns     []ColumnInfo     `json:"columns"`
	Indexes     []IndexInfo      `json:"indexes"`
	ForeignKeys []ForeignKeyInfo `json:"foreign_keys,omitempty"`
	Engine      string           `json:"engine,omitempty"`
	Charset     string           `json:"charset,omitempty"`
	Collation   string           `json:"collation,omitempty"`
	Comment     string           `json:"comment,omitempty"`
	DDL         string           `json:"ddl,omitempty"`
}

// ColumnSpec 是结构化编辑中的字段定义（与方言无关）。
type ColumnSpec struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Nullable      bool    `json:"nullable"`
	Default       *string `json:"default,omitempty"`
	Comment       string  `json:"comment,omitempty"`
	AutoIncrement bool    `json:"auto_increment,omitempty"`
	PrimaryKey    bool    `json:"primary_key,omitempty"`
	After         string  `json:"after,omitempty"`
}

// IndexSpec 是结构化编辑中的索引定义。
type IndexSpec struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique,omitempty"`
}

// TableDefinition 用于建表脚本生成。
type TableDefinition struct {
	Ref         ObjectRef    `json:"ref"`
	Columns     []ColumnSpec `json:"columns"`
	Indexes     []IndexSpec  `json:"indexes,omitempty"`
	Engine      string       `json:"engine,omitempty"`
	Charset     string       `json:"charset,omitempty"`
	Collation   string       `json:"collation,omitempty"`
	Comment     string       `json:"comment,omitempty"`
	IfNotExists bool         `json:"if_not_exists,omitempty"`
}

// DatabaseSpec 用于建库/改库脚本生成。
type DatabaseSpec struct {
	Name        string `json:"name"`
	Charset     string `json:"charset,omitempty"`
	Collation   string `json:"collation,omitempty"`
	IfNotExists bool   `json:"if_not_exists,omitempty"`
}

// SchemaChangeKind 枚举结构变更动作。
type SchemaChangeKind string

const (
	ChangeAddColumn      SchemaChangeKind = "add_column"
	ChangeModifyColumn   SchemaChangeKind = "modify_column"
	ChangeRenameColumn   SchemaChangeKind = "rename_column"
	ChangeDropColumn     SchemaChangeKind = "drop_column"
	ChangeAddIndex       SchemaChangeKind = "add_index"
	ChangeDropIndex      SchemaChangeKind = "drop_index"
	ChangeTableOptions   SchemaChangeKind = "table_options"
	ChangeCollectionOpts SchemaChangeKind = "collection_options"
)

// SchemaChange 是一条结构化变更指令。
type SchemaChange struct {
	Kind SchemaChangeKind `json:"kind"`

	Column  ColumnSpec `json:"column,omitempty"`
	OldName string     `json:"old_name,omitempty"`

	Index     IndexSpec `json:"index,omitempty"`
	IndexName string    `json:"index_name,omitempty"`

	Engine    string `json:"engine,omitempty"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
	Comment   string `json:"comment,omitempty"`
	// Validator 是 MongoDB 集合的 JSON Schema 校验器（Extended JSON 文本）。
	Validator string `json:"validator,omitempty"`
}

// Script 是可预览、可执行的方言脚本。
// Format 为 "sql" 时 Statements 是 SQL 文本；为 "mongo_command" 时是 JSON 命令文本。
// Rollback 为 best-effort，不保证与所有数据库一致。
type Script struct {
	Format     string   `json:"format,omitempty"`
	Statements []string `json:"statements"`
	Rollback   []string `json:"rollback,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

// ExecOutcome 是单条语句的执行结果。
type ExecOutcome struct {
	Statement    string `json:"statement"`
	RowsAffected int64  `json:"rows_affected,omitempty"`
	Message      string `json:"message,omitempty"`
}

// ApplyResult 汇总脚本执行结果。
type ApplyResult struct {
	Outcomes []ExecOutcome `json:"outcomes"`
}

// QueryLanguage 区分不同数据库的查询输入形态。
type QueryLanguage string

const (
	LangSQL           QueryLanguage = "sql"
	LangMongoFilter   QueryLanguage = "mongo_filter"
	LangMongoPipeline QueryLanguage = "mongo_pipeline"
)

// Query 是一次查询请求。Collection 仅对 MongoDB 有意义。
type Query struct {
	Language QueryLanguage `json:"language"`
	Text     string        `json:"text"`
	// Ref 非空且 Text 为空时表示「预览该对象的数据」，由各模块自行生成方言语句。
	Ref        ObjectRef `json:"ref,omitempty"`
	Database   string    `json:"database,omitempty"`
	Collection string    `json:"collection,omitempty"`
	MaxRows    int       `json:"max_rows,omitempty"`
	TimeoutSec int       `json:"timeout_sec,omitempty"`
	ReadOnly   bool      `json:"read_only,omitempty"`
}

// ValueKind 描述结果单元格的展示类型。
type ValueKind string

const (
	ValueText   ValueKind = "text"
	ValueNumber ValueKind = "number"
	ValueBinary ValueKind = "binary"
	ValueJSON   ValueKind = "json"
	ValueNull   ValueKind = "null"
)

// Cell 是结果集中的一个值。
type Cell struct {
	Kind   ValueKind `json:"kind"`
	Text   string    `json:"text"`
	Binary []byte    `json:"-"`
	Null   bool      `json:"null,omitempty"`
}

func NewTextCell(s string) Cell { return Cell{Kind: ValueText, Text: s} }

func NewNullCell() Cell { return Cell{Kind: ValueNull, Null: true, Text: "NULL"} }

func NewBinaryCell(b []byte) Cell {
	return Cell{Kind: ValueBinary, Binary: b, Text: hexPreview(b)}
}

// Display 返回适合直接渲染的短文本。
func (c Cell) Display() string {
	if c.Null {
		return "NULL"
	}
	return c.Text
}

// Row 是一行结果。实现方每次 Row() 必须返回新分配的切片。
type Row []Cell

// Column 描述结果集的一列。
type Column struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// ServerMessage 承载服务端的提示、警告与影响行数说明。
type ServerMessage struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// QuerySummary 是一次查询执行的汇总信息。
type QuerySummary struct {
	Rows       int64           `json:"rows"`
	Affected   int64           `json:"affected"`
	DurationMs int64           `json:"duration_ms"`
	Pages      int             `json:"pages"`
	Truncated  bool            `json:"truncated,omitempty"`
	Messages   []ServerMessage `json:"messages,omitempty"`
}

// ResultPage 对应一个结果集（T-SQL 批与存储过程可能有多个）。
// JSONDocuments 保存文档型数据库的原始文档，用于 JSON 树视图。
type ResultPage struct {
	Columns       []Column `json:"columns"`
	Rows          []Row    `json:"rows"`
	JSONDocuments []string `json:"json_documents,omitempty"`
	Truncated     bool     `json:"truncated,omitempty"`
}

// QueryOutcome 是面向表现层的完整查询结果。
type QueryOutcome struct {
	Pages   []ResultPage `json:"pages"`
	Summary QuerySummary `json:"summary"`
}

// ExecResult 用于无结果集的语句（DDL / DML）。
type ExecResult struct {
	Statement    string `json:"statement"`
	RowsAffected int64  `json:"rows_affected"`
	LastInsertID int64  `json:"last_insert_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

// EditKind 是结果集数据编辑的动作。
type EditKind string

const (
	EditUpdate EditKind = "update"
	EditDelete EditKind = "delete"
	EditInsert EditKind = "insert"
)

// RowEdit 描述对一行的变更。Key 为原始主键值，Values 为新值；
// map 中 nil 指针表示 SQL NULL / BSON null。
type RowEdit struct {
	Kind   EditKind           `json:"kind"`
	Key    map[string]*string `json:"key,omitempty"`
	Values map[string]*string `json:"values,omitempty"`
}

// EditRequest 是一次数据编辑回写请求，由各数据库模块翻译成参数化语句。
type EditRequest struct {
	Ref         ObjectRef `json:"ref"`
	KeyColumns  []string  `json:"key_columns,omitempty"`
	Edits       []RowEdit `json:"edits"`
	Transaction bool      `json:"transaction,omitempty"`
}

// EditResult 汇总回写结果，Statements 用于预览与审计。
type EditResult struct {
	Applied    int      `json:"applied"`
	Statements []string `json:"statements,omitempty"`
}

const (
	// DefaultMaxRows 是默认的最大预览行数，防止大结果集撑爆内存。
	DefaultMaxRows = 1000
	// ResultBatchSize 是流式推送给表现层的每批行数。
	ResultBatchSize = 200
)

func hexPreview(b []byte) string {
	const limit = 32
	if len(b) > limit {
		return "0x" + hex.EncodeToString(b[:limit]) + "… (" + fmt.Sprintf("%d", len(b)) + " bytes)"
	}
	return "0x" + hex.EncodeToString(b)
}
