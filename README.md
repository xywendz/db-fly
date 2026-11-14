# db-fly

> 模块化、多数据库、跨平台的数据库客户端。**同一套业务内核**提供三种形态：Windows 桌面 GUI、跨平台 Web 服务（容器部署）、纯 Go 命令行 CLI；连接定义、查询历史与收藏在 GUI 与 CLI 之间共享。

---

## 特性

- **一套内核，三种形态**
  - 桌面 GUI：Wails v3 + Vue3（Windows，纯 Go 编译，无需 gcc，运行时依赖 WebView2）
  - Web 服务：Vue3 前端内嵌 Go 二进制，Docker 一键部署，支持多用户与操作审计
  - 命令行：纯 Go，可 `CGO_ENABLED=0` 静态编译，适用于 Linux 服务器
- **统一连接管理**：连接配置 CRUD、连通性测试、分组 / 颜色 / 只读 / 超时 / TLS / 附加参数；类型下拉与功能菜单由后端能力注册表动态生成。
- **结构化浏览与编辑**：连接 → 库（SQL Server / PostgreSQL 含 schema）→ 表 / 视图 / 集合 / 键 的树形浏览；字段、索引、外键、建表 DDL / 集合定义详情；结构变更走「设计器 → 方言脚本 → 预览 → 确认」。
- **多查询语言**：SQL、MongoDB filter / aggregation pipeline、Elasticsearch DSL、Redis 原生命令，统一的结果集呈现（多结果集页签、JSON 文档树、行数上限与截断提示）。
- **安全默认**：危险操作（DDL、数据回写）一律先生成方言脚本并预览、再显式确认；密码不写入配置文件；写语句默认拒绝，只读连接可全局生效；回写参数化。
- **流式有界**：结果集流式消费，默认最多 1000 行。
- **导入导出**：CSV / TSV / Excel（`.xlsx`/`.xlsm`）/ JSON 数组 / NDJSON / SQL；文档型数据库（MongoDB、Elasticsearch）支持**无损 JSON 文档往返**（数字 / 布尔 / 嵌套类型不丢失）。
- **多用户与审计（Web 模式）**：账号注册 / 登录、按用户隔离数据、操作审计日志。

## 支持的数据库

| 类型 | `kind` | 默认端口 | 查询语言 | 内核依赖 |
| --- | --- | --- | --- | --- |
| MySQL | `mysql` | 3306 | SQL | `go-sql-driver/mysql` |
| SQL Server | `sqlserver` | 1433 | SQL (T-SQL) | `microsoft/go-mssqldb` |
| PostgreSQL | `postgresql` | 5432 | SQL | `jackc/pgx/v5` |
| Oracle | `oracle` | 1521 | SQL | `sijms/go-ora/v2` |
| SQLite | `sqlite` | —（文件型） | SQL | `modernc.org/sqlite`（纯 Go） |
| MongoDB | `mongodb` | 27017 | filter / pipeline | `mongo-driver/v2` |
| Redis | `redis` | 6379 | 原生命令 | `go-redis/v9` |
| Elasticsearch | `elasticsearch` | 9200 | JSON DSL | 标准库 REST（零依赖） |

各模块通过 `CapabilitySet` 声明能力，表现层据此决定可用的菜单与流程；通用层不出现任何 `switch Kind` 或 SQL 拼接。

---

## 快速开始

### 桌面 GUI（Windows）

构建或获取 `db-fly-gui.exe`，双击启动。需要 **WebView2 Runtime**（Windows 11 自带；Windows 10 需安装「Microsoft Edge WebView2 Runtime」）。新建连接 → 选择数据库类型 → 填写主机 / 端口 / 账号 → 连接后即可浏览对象、执行查询、编辑数据。

### Web 服务（Docker）

```bash
docker build -t db-fly-server .
docker run -d --name db-fly -p 8080:8080 \
  -v db-fly-data:/data \
  -e DB_FLY_WEB_TOKEN=change-me \
  db-fly-server
```

打开 `http://localhost:8080/login` 注册首个账号（`DB_FLY_WEB_TOKEN` 为邀请码，公网部署务必设置）。如需关闭公开注册，追加 `-e DB_FLY_ALLOW_SIGNUP=false`。

### 命令行 CLI

```bash
# 新增连接（同名即更新；--params 为 key=value 逗号分隔）
dbctl conn add --name dev --kind postgresql --host localhost --port 5432 --user postgres --password secret
# Elasticsearch 额外参数示例
dbctl conn add --name es --kind elasticsearch --host localhost --port 9200 \
  --user elastic --password change-me --tls false \
  --params "auth=basic,namespaceSeparator=."
dbctl conn list

# 进入交互式查询 REPL
dbctl repl --conn dev

# 查看运行环境与已注册模块
dbctl info
```

常用子命令：`conn`（list/add/test/rm）、`db`（list/create/alter）、`schema`（show/ddl/alter/create-table/drop/apply）、`query`、`data browse`、`edit`、`import`、`history`、`favorite`、`repl`、`info`。任意命令加 `--help` 查看参数。

---

## 从源码构建

### 环境要求

| 工具 | 版本 | 说明 |
| --- | --- | --- |
| Go | 1.26.0+ | 编译后端（纯 Go） |
| Node.js / npm | 20+ | 构建 Vue 前端 |
| wails3 CLI | `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0` | 图标 / PE 资源生成（GUI 可选） |
| Task（Taskfile） | 任意 | `wails3 build` 的任务运行时 |
| WebView2 Runtime | Windows | GUI 运行期渲染 |

### 构建命令（均在仓库根目录执行）

```bash
# 桌面 GUI（Windows）：前端构建 + 图标/syso + 纯 Go 链接
task build                 # → dist/db-fly-gui.exe（PowerShell）
# 或仅环境/代码校验（gofmt/build/vet/test + vue-tsc + vite build）
task verify

# Web 模式（全平台）：前端 web 产物嵌入 Go 二进制
task build:web            # → dist/db-fly-server
# 构建两者
task build:all

# 纯命令行（任意平台）
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/dbctl ./cmd/dbctl
```

前端自身命令：`cd frontend && npm install`，开发热更新 `npm run dev`（GUI）/`npm run dev:web`（Web）；类型检查 `npm run typecheck`；产物构建 `npm run build` / `npm run build:web`。GUI 与 Web 通过编译期别名 `@/runtime` 物理隔离，GUI 产物不含 fetch 代码、Web 产物不含 `@wailsio/runtime`。

---

## 连接配置

连接定义（持久化结构，密码不落盘，由凭证库注入）：

| 字段 | 说明 |
| --- | --- |
| `name` | 显示名称（唯一） |
| `kind` | 数据库类型，见上表 |
| `host` / `port` | 主机与端口（文件型如 SQLite 不需要） |
| `user` / `password` | 账号与密码（仅在运行时 / 配置显式填写时存在） |
| `database` | 默认库（可选） |
| `tls_mode` | `false`（不加密）/ `true`（加密并校验证书）/ `skip-verify`（加密但跳过校验） |
| `read_only` | 只读连接，拒绝所有写操作 |
| `timeout_sec` | 连接 / 语句超时（默认 15 秒） |
| `group` / `color` | 连接分组与颜色标记 |
| `params` | 各模块自有选项（JSON 字符串字典） |

示例：

```jsonc
// MySQL
{ "name": "mysql-prod", "kind": "mysql", "host": "10.0.0.5", "port": 3306,
  "user": "app", "password": "secret", "tls_mode": "true" }

// PostgreSQL（指定默认 schema 可用 database 或连接后选择）
{ "name": "pg-dev", "kind": "postgresql", "host": "localhost", "port": 5432,
  "user": "postgres", "password": "secret", "database": "shop" }

// SQLite（文件型）
{ "name": "local", "kind": "sqlite", "host": "/var/data/app.db" }

// MongoDB
{ "name": "mongo", "kind": "mongodb", "host": "localhost", "port": 27017,
  "user": "root", "password": "secret", "params": { "authSource": "admin" } }

// Redis
{ "name": "cache", "kind": "redis", "host": "localhost", "port": 6379, "password": "" }

// Elasticsearch（Basic 认证）
{ "name": "es-prod", "kind": "elasticsearch", "host": "localhost", "port": 9200,
  "user": "elastic", "password": "change-me", "tls_mode": "false",
  "params": { "auth": "basic", "namespaceSeparator": "." } }

// Elasticsearch（API Key 认证）
{ "name": "es-secure", "kind": "elasticsearch", "host": "es.example.com", "port": 9200,
  "user": "", "password": "id:base64-apikey", "tls_mode": "true",
  "params": { "auth": "apikey" } }
```

### Elasticsearch 适配要点

- **命名空间映射**：ES 无库 / 表概念，索引名 `shop.users` 映射为逻辑库 `shop`、索引 `users`；无前缀的索引归 `default` 库；`db-fly` 管理的索引模板 `dbfly_<db>`（索引模式 `<db>.*`）让「空库」在树中可见。
- **查询**：使用 Elasticsearch `_search` 请求体作为查询内容（语言 `es_dsl`），可带 `index` 字段或选择库 / 索引；聚合桶作为第二个结果集返回。也支持原生 REST 命令（`Exec`）。
- **建库 / 建索引**：「新建逻辑库」创建组合索引模板（ES 7.8+），7.7 及以前自动降级到 legacy `_template`；「新建索引」生成 `PUT /<index>`，字段映射为 `mappings.properties`，支持分片 / 副本 / `dynamic` / 别名。
- **数据编辑**：增删改走 `POST /_bulk`（NDJSON）；导入时优先使用原始 JSON 文档文本，保留标量类型与嵌套结构，NDJSON 导出 → 导入可无损往返。
- **导入 / 导出**：支持 CSV / TSV / Excel / JSON / NDJSON / SQL；ES 额外提供 **NDJSON**（每行原始文档，可回导）与 **ES Bulk**（可直接 `curl --data-binary @file` 回放），并支持自定义 DSL 作为导出数据源。

---

## 目录结构

```
db-fly/
├── cmd/
│   ├── db-fly-gui/      # 桌面 GUI 入口（//go:build windows）
│   ├── db-fly-server/   # Web 服务入口（全平台，纯 Go）
│   └── dbctl/           # 命令行 CLI 入口（纯 Go）
├── internal/
│   ├── domain/          # 实体、值对象、端口契约（仅依赖标准库）
│   ├── app/             # 用例层：连接、查询、结构、数据、导入导出编排
│   ├── bridge/          # Wails 服务层，把 app 暴露给前端
│   ├── httpapi/         # Web 模式 REST / SSE / 上传
│   ├── auth/            # 多用户账号与隔离（Web 模式）
│   ├── cli/             # 命令行命令树（cobra）
│   ├── provider/        # 各数据库业务模块
│   │   ├── all/         # 注册中心（import 全部 provider 完成自注册）
│   │   ├── mysql/ sqlserver/ postgresql/ oracle/ sqlite/
│   │   ├── mongodb/ redis/
│   │   └── elasticsearch/
│   ├── platform/        # SQL 构造、导出等跨模块实现
│   └── webui/           # 嵌入的 Web 前端产物（//go:embed）
├── frontend/            # Vue3 + TypeScript 前端（GUI / Web 双模式，@/runtime 隔离）
├── build/               # 构建脚本（gui.ps1 / web.ps1 / verify.ps1 等）
├── Taskfile.yml         # 构建任务定义
├── wails.json           # Wails 配置
├── Dockerfile           # Web 模式多阶段镜像
└── project.md           # 详细设计说明
```

---

## 开发与测试

```bash
# 后端：格式化、编译、静态检查、单元测试
gofmt -l ./internal ./cmd
go build ./...
go vet ./...
go test ./...

# 前端：类型检查与生产构建
cd frontend && npm run typecheck && npm run build

# 一体化校验（等价于 task verify）
powershell -NoProfile -ExecutionPolicy Bypass -File ./build/verify.ps1
```

集成测试（需真实数据库）通常带构建标签，例如 `//go:build integration`，运行方式见各模块测试文件头部。

---

## 设计文档

完整的业务设计、架构说明、能力矩阵与构建 / 部署细节见 [`project.md`](./project.md)。
