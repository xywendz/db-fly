//go:build custom_providers && !db_elasticsearch && !db_mongo && !db_mssql && !db_mysql && !db_oracle && !db_postgres && !db_redis && !db_sqlite

package all

// custom_providers 模式下必须至少选择一个 db_* 标签，否则没有注册任何数据库类型。
// 用类型错误在编译期阻止这种无效构建，给出明确提示。
var _ int = "请至少指定一个 db_* 构建标签，例如 -tags \"custom_providers db_mysql\""
