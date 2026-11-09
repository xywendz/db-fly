// Package all 负责装配全部数据库业务模块。
// 表现层只需 blank import 本包即可完成注册；各模块之间互不依赖。
package all

import (
	_ "github.com/dbclient/dbclient/internal/provider/mongo"
	_ "github.com/dbclient/dbclient/internal/provider/mssql"
	_ "github.com/dbclient/dbclient/internal/provider/mysql"
)
