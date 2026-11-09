// Package all 负责装配全部数据库业务模块。
// 表现层只需 blank import 本包即可完成注册；各模块之间互不依赖。
package all

import (
	_ "github.com/db-fly/db-fly/internal/provider/mongo"
	_ "github.com/db-fly/db-fly/internal/provider/mssql"
	_ "github.com/db-fly/db-fly/internal/provider/mysql"
	_ "github.com/db-fly/db-fly/internal/provider/oracle"
	_ "github.com/db-fly/db-fly/internal/provider/postgres"
	_ "github.com/db-fly/db-fly/internal/provider/redis"
	_ "github.com/db-fly/db-fly/internal/provider/sqlite"
)
