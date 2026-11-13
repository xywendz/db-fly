import type { ConnectionConfig, Kind } from "@/types/domain";

/** 每个数据库类型专属的连接表单配置：新建连接时按类型展示不同字段。 */
export interface ParamField {
  key: string;
  label: string;
  placeholder?: string;
  hint?: string;
}

export interface KindForm {
  kind: Kind;
  label: string;
  icon: string;
  badge: string;
  summary: string;
  tips: string[];
  fileBased?: boolean;
  hostLabel: string;
  hostPlaceholder: string;
  databaseLabel: string;
  databasePlaceholder: string;
  showPort: boolean;
  showUser: boolean;
  showPassword: boolean;
  showDatabase: boolean;
  showTLS: boolean;
  extras: ParamField[];
  defaults: Partial<ConnectionConfig>;
}

export interface TLSOption {
  value: string;
  label: string;
}

export const TLS_OPTIONS: TLSOption[] = [
  { value: "", label: "默认（由驱动决定）" },
  { value: "false", label: "禁用 TLS" },
  { value: "true", label: "启用 TLS" },
  { value: "skip-verify", label: "启用 TLS，不校验证书" },
  { value: "preferred", label: "优先 TLS" },
];

export const KIND_ORDER: Kind[] = [
  "mysql",
  "postgresql",
  "sqlserver",
  "oracle",
  "sqlite",
  "mongodb",
  "redis",
];

export const KIND_FORMS: Record<Kind, KindForm> = {
  mysql: {
    kind: "mysql",
    label: "MySQL / MariaDB",
    icon: "database",
    badge: "badge-info",
    summary: "关系型数据库，支持库/表结构、SQL 查询与事务化数据编辑。",
    tips: ["默认端口 3306", "常用参数 charset=utf8mb4", "支持 TLS 与只读连接"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "默认数据库",
    databasePlaceholder: "例如 app_db（可选）",
    showPort: true,
    showUser: true,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [{ key: "charset", label: "字符集", placeholder: "utf8mb4" }],
    defaults: { host: "127.0.0.1", port: 3306, user: "root", params: { charset: "utf8mb4" } },
  },
  postgresql: {
    kind: "postgresql",
    label: "PostgreSQL",
    icon: "database",
    badge: "badge-info",
    summary: "支持 schema、多结果集与丰富 DDL 的开源关系型数据库。",
    tips: ["默认端口 5432", "可用 search_path 指定默认 schema", "TLS 支持 disable/require/prefer"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "默认数据库 *",
    databasePlaceholder: "postgres",
    showPort: true,
    showUser: true,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [{ key: "search_path", label: "默认 schema", placeholder: "public" }],
    defaults: { host: "127.0.0.1", port: 5432, user: "postgres", database: "postgres", params: {} },
  },
  sqlserver: {
    kind: "sqlserver",
    label: "SQL Server",
    icon: "server",
    badge: "badge-danger",
    summary: "微软 SQL Server，支持 Windows 集成验证之外的账号密码连接。",
    tips: ["默认端口 1433", "TLS 对应 encrypt 参数", "可附加 instance / app name 等参数"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "默认数据库",
    databasePlaceholder: "master",
    showPort: true,
    showUser: true,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [
      { key: "app name", label: "应用名称", placeholder: "db-fly" },
      { key: "instance", label: "实例名", placeholder: "可选" },
    ],
    defaults: { host: "127.0.0.1", port: 1433, user: "sa", database: "master", params: {} },
  },
  oracle: {
    kind: "oracle",
    label: "Oracle",
    icon: "server",
    badge: "badge-warn",
    summary: "通过服务名或 SID 连接 Oracle 数据库。",
    tips: ["默认端口 1521", "必须提供服务名 / SID", "服务名可写在默认数据库或下方参数中"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "服务名 / SID *",
    databasePlaceholder: "ORCL 或 XEPDB1",
    showPort: true,
    showUser: true,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [{ key: "service", label: "服务名（优先于上方）", placeholder: "XEPDB1" }],
    defaults: { host: "127.0.0.1", port: 1521, user: "system", params: {} },
  },
  sqlite: {
    kind: "sqlite",
    label: "SQLite",
    icon: "file",
    badge: "",
    summary: "本地文件型数据库，无需主机、端口与账号密码。",
    tips: ["填入 .db 文件绝对路径", "填 :memory: 使用内存数据库", "文件不存在时会自动创建"],
    fileBased: true,
    hostLabel: "数据库文件路径 *",
    hostPlaceholder: "D:\\data\\app.db",
    databaseLabel: "默认库",
    databasePlaceholder: "可选",
    showPort: false,
    showUser: false,
    showPassword: false,
    showDatabase: false,
    showTLS: false,
    extras: [{ key: "file", label: "文件路径参数（可选）", placeholder: "也可填在上方路径栏" }],
    defaults: { host: "", port: 0, params: {} },
  },
  mongodb: {
    kind: "mongodb",
    label: "MongoDB",
    icon: "layers",
    badge: "badge-ok",
    summary: "文档型数据库，按集合浏览 JSON 文档，使用 Mongo 过滤语法。",
    tips: ["默认端口 27017", "账号密码可选，authSource 默认 admin", "支持 replicaSet 参数"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "默认数据库 *",
    databasePlaceholder: "test",
    showPort: true,
    showUser: true,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [
      { key: "authSource", label: "认证数据库", placeholder: "admin" },
      { key: "replicaSet", label: "副本集名称", placeholder: "可选" },
    ],
    defaults: { host: "127.0.0.1", port: 27017, database: "test", params: { authSource: "admin" } },
  },
  redis: {
    kind: "redis",
    label: "Redis",
    icon: "key",
    badge: "badge-danger",
    summary: "键值数据库，按 DB 编号浏览键空间。",
    tips: ["默认端口 6379", "默认数据库填 DB 编号（0-15）", "密码填在密码栏，用户名走 ACL 参数"],
    hostLabel: "主机地址 *",
    hostPlaceholder: "127.0.0.1",
    databaseLabel: "DB 编号",
    databasePlaceholder: "0",
    showPort: true,
    showUser: false,
    showPassword: true,
    showDatabase: true,
    showTLS: true,
    extras: [
      { key: "username", label: "ACL 用户名", placeholder: "可选" },
      { key: "clientname", label: "客户端名称", placeholder: "可选" },
    ],
    defaults: { host: "127.0.0.1", port: 6379, database: "0", params: {} },
  },
};

export function formOf(kind: string): KindForm {
  return KIND_FORMS[kind as Kind] ?? KIND_FORMS.mysql;
}

/** 按类型生成一份干净的连接配置，避免切换到别的类型时残留字段。 */
export function blankFor(kind: string): ConnectionConfig {
  const cfg = formOf(kind);
  return {
    id: "",
    name: "",
    kind: cfg.kind,
    host: cfg.defaults.host ?? "",
    port: cfg.defaults.port ?? 0,
    user: cfg.defaults.user ?? "",
    database: cfg.defaults.database ?? "",
    params: { ...(cfg.defaults.params ?? {}) },
    timeout_sec: 10,
  };
}
