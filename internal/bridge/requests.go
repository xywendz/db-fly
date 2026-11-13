package bridge

import "github.com/db-fly/db-fly/internal/domain"

// 以下 DTO 用于把「连接名 + 业务参数」打包成单一 JSON 参数，便于前端一次性传入，
// 也避免 wails 方法出现过多位置参数导致前端调用易错。

// QueryRequest 包装一次查询请求：连接名 + 查询对象。
type QueryRequest struct {
	Conn  string       `json:"conn"`
	Query domain.Query `json:"query"`
}

// ExecRequest 包装一条无结果集语句的执行请求。
type ExecRequest struct {
	Conn      string `json:"conn"`
	Statement string `json:"statement"`
	Database  string `json:"database,omitempty"`
}

// ScriptRequest 包装一段脚本（多条语句）按顺序执行的请求。
type ScriptRequest struct {
	Conn  string       `json:"conn"`
	Text  string       `json:"text"`
	Query domain.Query `json:"query"`
}

// ObjectRequest 包装一次针对某个对象的请求（连接名 + 对象引用）。
type ObjectRequest struct {
	Conn string           `json:"conn"`
	Ref  domain.ObjectRef `json:"ref"`
}

// ImportRequest 包装一次文件导入请求。
type ImportRequest struct {
	Conn        string           `json:"conn"`
	Ref         domain.ObjectRef `json:"ref"`
	Path        string           `json:"path"`
	Format      string           `json:"format"`
	Sheet       string           `json:"sheet"`
	HasHeader   bool             `json:"has_header"`
	BatchSize   int              `json:"batch_size"`
	Transaction bool             `json:"transaction"`
	DryRun      bool             `json:"dry_run"`
}

// AppInfo 描述运行时信息，供「关于 / 设置」页展示。
type AppInfo struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	DataDir     string   `json:"data_dir"`
	SecretsName string   `json:"secrets_name"`
	Kinds       []string `json:"kinds"`
}

// ImportOutcomeDTO 是 app.ImportOutcome 的前端镜像，移除了不可序列化的内部字段。
type ImportOutcomeDTO struct {
	Format     string   `json:"format"`
	Rows       int      `json:"rows"`
	Statements int      `json:"statements"`
	Preview    []string `json:"preview,omitempty"`
	Applied    int      `json:"applied"`
}
