//go:build !custom_providers

// Package all 负责装配全部数据库业务模块。
// 表现层只需 blank import 本包即可完成注册；各模块之间互不依赖。
//
// 默认构建包含全部数据库类型。若只需部分类型，可用构建标签裁剪：
//
//	-tags "custom_providers db_mysql db_sqlite"
//
// 可选标签：db_elasticsearch / db_mongo / db_mssql / db_mysql /
// db_oracle / db_postgres / db_redis / db_sqlite。
// 前端的连接类型下拉由后端 ListKinds 驱动，会自动只显示已注册的类型。
package all

import (
	_ "github.com/xywendz/db-fly/internal/provider/elasticsearch"
	_ "github.com/xywendz/db-fly/internal/provider/mongo"
	_ "github.com/xywendz/db-fly/internal/provider/mssql"
	_ "github.com/xywendz/db-fly/internal/provider/mysql"
	_ "github.com/xywendz/db-fly/internal/provider/oracle"
	_ "github.com/xywendz/db-fly/internal/provider/postgres"
	_ "github.com/xywendz/db-fly/internal/provider/redis"
	_ "github.com/xywendz/db-fly/internal/provider/sqlite"
)
