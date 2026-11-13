// 与 internal/domain 一一对应的前端类型（JSON 标签对齐）。
// 用于在 UI 层获得类型安全；运行期由 wails 绑定自动（反）序列化。

export type Kind =
  | "mysql"
  | "sqlserver"
  | "mongodb"
  | "postgresql"
  | "oracle"
  | "sqlite"
  | "redis";

export type ObjectType = "table" | "view" | "collection" | "key";

export type Capability =
  | "create_database"
  | "alter_database"
  | "create_table"
  | "alter_table"
  | "drop_object"
  | "indexes"
  | "foreign_keys"
  | "schemas"
  | "sql"
  | "mongo_filter"
  | "json_tree"
  | "explain"
  | "multi_result"
  | "transactions"
  | "data_edit"
  | "table_filter"
  | "show_create"
  | "truncate_table"
  | "empty_table"
  | "key_values";

export interface ConnectionConfig {
  id: string;
  name: string;
  kind: Kind;
  host: string;
  port: number;
  user?: string;
  database?: string;
  params?: Record<string, string>;
  tls_mode?: string;
  read_only?: boolean;
  timeout_sec?: number;
  group?: string;
  color?: string;
}

export interface KindInfo {
  kind: Kind;
  label: string;
  default_port: number;
  relational: boolean;
  file_based: boolean;
  capabilities: Capability[];
  languages: string[];
}

export interface ServerInfo {
  kind: Kind;
  version: string;
  edition?: string;
  host?: string;
}

export interface DatabaseInfo {
  name: string;
  charset?: string;
  collation?: string;
  system?: boolean;
}

export interface ObjectInfo {
  database: string;
  schema?: string;
  name: string;
  type: ObjectType;
  comment?: string;
  rows?: number;
}

export interface ObjectRef {
  database: string;
  schema?: string;
  name: string;
}

export interface ColumnInfo {
  position: number;
  name: string;
  data_type: string;
  raw_type?: string;
  length?: number;
  nullable: boolean;
  default?: string;
  has_default?: boolean;
  primary_key?: boolean;
  auto_increment?: boolean;
  comment?: string;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  unique?: boolean;
  primary?: boolean;
  index_type?: string;
}

export interface ForeignKeyInfo {
  name: string;
  columns: string[];
  ref_database?: string;
  ref_table: string;
  ref_columns: string[];
}

export interface TableSchema {
  ref: ObjectRef;
  columns: ColumnInfo[];
  indexes: IndexInfo[];
  foreign_keys?: ForeignKeyInfo[];
  engine?: string;
  charset?: string;
  collation?: string;
  comment?: string;
  ddl?: string;
}

export interface Cell {
  kind: "text" | "number" | "binary" | "json" | "null";
  text: string;
  null?: boolean;
}

export type Row = Cell[];

export interface ResultColumn {
  name: string;
  type?: string;
}

export interface ServerMessage {
  level: string;
  text: string;
}

export interface QuerySummary {
  rows: number;
  affected: number;
  duration_ms: number;
  pages: number;
  truncated?: boolean;
  messages?: ServerMessage[];
}

export interface ResultPage {
  columns: ResultColumn[];
  rows: Row[];
  json_documents?: string[];
  truncated?: boolean;
}

export interface QueryOutcome {
  pages: ResultPage[];
  summary: QuerySummary;
}

export interface ExecResult {
  statement: string;
  rows_affected: number;
  last_insert_id?: number;
  message?: string;
}

export interface ColumnSpec {
  name: string;
  type: string;
  nullable: boolean;
  default?: string | null;
  comment?: string;
  auto_increment?: boolean;
  primary_key?: boolean;
}

export interface IndexSpec {
  name: string;
  columns: string[];
  unique?: boolean;
}

export interface TableDefinition {
  ref: ObjectRef;
  columns: ColumnSpec[];
  indexes?: IndexSpec[];
  engine?: string;
  charset?: string;
  collation?: string;
  comment?: string;
  if_not_exists?: boolean;
}

export interface DatabaseSpec {
  name: string;
  charset?: string;
  collation?: string;
  if_not_exists?: boolean;
}

export type SchemaChangeKind =
  | "add_column"
  | "modify_column"
  | "rename_column"
  | "rename_table"
  | "drop_column"
  | "add_index"
  | "drop_index"
  | "table_options"
  | "collection_options";

export interface SchemaChange {
  kind: SchemaChangeKind;
  column?: ColumnSpec;
  old_name?: string;
  new_name?: string;
  index?: IndexSpec;
  index_name?: string;
  engine?: string;
  charset?: string;
  collation?: string;
  comment?: string;
  validator?: string;
}

export interface Script {
  format?: string;
  statements: string[];
  rollback?: string[];
  warnings?: string[];
  database?: string;
}

export interface ApplyResult {
  outcomes: ExecOutcome[];
}

export interface ExecOutcome {
  statement: string;
  rows_affected?: number;
  message?: string;
}

export interface FilterCondition {
  column: string;
  operator: string;
  value?: string;
  join?: string;
}

export interface SortTerm {
  column: string;
  desc?: boolean;
}

export interface Query {
  language?: string;
  text?: string;
  ref?: ObjectRef;
  database?: string;
  collection?: string;
  filter?: string;
  sort?: string;
  conditions?: FilterCondition[];
  order_terms?: SortTerm[];
  offset?: number;
  max_rows?: number;
  timeout_sec?: number;
  read_only?: boolean;
}

export interface QueryRequest {
  conn: string;
  query: Query;
}

export interface ExecRequest {
  conn: string;
  statement: string;
  database?: string;
}

export interface ScriptRequest {
  conn: string;
  text: string;
  query: Query;
}

export interface ObjectRequest {
  conn: string;
  ref: ObjectRef;
}

export interface EditRequest {
  ref: ObjectRef;
  key_columns?: string[];
  edits: RowEdit[];
  transaction?: boolean;
}

export interface RowEdit {
  kind: "update" | "delete" | "insert";
  key?: Record<string, string | null>;
  values?: Record<string, string | null>;
}

export interface EditResult {
  applied: number;
  statements?: string[];
}

export interface ImportRequest {
  conn: string;
  ref: ObjectRef;
  path: string;
  format: string;
  sheet?: string;
  has_header?: boolean;
  batch_size?: number;
  transaction?: boolean;
  dry_run?: boolean;
}

export interface ImportOutcomeDTO {
  format: string;
  rows: number;
  statements: number;
  preview?: string[];
  applied: number;
}

export interface AppInfo {
  name: string;
  version: string;
  data_dir: string;
  secrets_name: string;
  kinds: string[];
}

// ===== 配置层（与 internal/platform/config 的 JSON 标签对齐） =====

export interface QueryHistoryEntry {
  id: string;
  at: string;
  connection?: string;
  kind?: string;
  action?: string;
  database?: string;
  collection?: string;
  language?: string;
  text?: string;
  rows?: number;
  duration_ms?: number;
  ok: boolean;
  error?: string;
}

export interface SavedQuery {
  id: string;
  name: string;
  folder?: string;
  kind?: string;
  connection?: string;
  database?: string;
  collection?: string;
  language?: string;
  text: string;
  updated_at: string;
}

export interface Preferences {
  max_rows: number;
  theme: string;
  language: string;
  confirm_dangerous: boolean;
}
