import type {
  AppInfo,
  Capability,
  ConnectionConfig,
  DatabaseInfo,
  DatabaseSpec,
  EditRequest,
  EditResult,
  ExecRequest,
  ImportOutcomeDTO,
  ImportRequest,
  Kind,
  KindInfo,
  ObjectInfo,
  ObjectRef,
  Query,
  QueryOutcome,
  QueryRequest,
  ResultPage,
  SchemaChange,
  Script,
  ScriptRequest,
  ServerInfo,
  TableDefinition,
  TableSchema,
} from "@/types/domain";
import { raw } from "./bridge";

// 统一的错误类型，便于 UI 区分展示。
export class DbError extends Error {
  code?: string;
  constructor(message: string, code?: string) {
    super(message);
    this.name = "DbError";
    this.code = code;
  }
}

async function call<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e: any) {
    const msg = e?.message ?? String(e);
    const code = e?.code;
    throw new DbError(msg, code);
  }
}

/**
 * 空结果集在 Go 侧可能把 columns / rows 序列化为 null。
 * 统一规整为数组，避免界面读取 .length 时抛异常。
 */
function normalizeOutcome(res: QueryOutcome | null | undefined): QueryOutcome {
  const pages: ResultPage[] = (Array.isArray(res?.pages) ? res!.pages : []).map((p: ResultPage) => ({
    columns: Array.isArray(p?.columns) ? p.columns : [],
    rows: Array.isArray(p?.rows) ? p.rows : [],
    json_documents: Array.isArray(p?.json_documents) ? p.json_documents : [],
    truncated: !!p?.truncated,
  }));
  return {
    pages,
    summary: res?.summary ?? { rows: 0, affected: 0, duration_ms: 0, pages: 0 },
  };
}

// ===== 连接管理 =====
export const listConnections = () => call<ConnectionConfig[]>(raw.ListConnections());
export const saveConnection = (
  c: ConnectionConfig,
  password: string,
  replacePassword: boolean,
) => call<ConnectionConfig>(raw.SaveConnection(c, password, replacePassword));
export const removeConnection = (id: string) => call<boolean>(raw.RemoveConnection(id));
export const closeConnection = (conn: string) => call<void>(raw.CloseConnection(conn));
export const openConnections = () => call<string[]>(raw.OpenConnections());
export const testConnection = (c: ConnectionConfig, password: string) =>
  call<ServerInfo>(raw.TestConnection(c, password));
export const testSavedConnection = (id: string) => call<ServerInfo>(raw.TestSavedConnection(id));
export const listKinds = () => call<KindInfo[]>(raw.ListKinds());
export const kindCapabilities = (kind: Kind) =>
  call<Capability[]>(raw.KindCapabilities(kind));
export const validateConnection = (c: ConnectionConfig) => call<boolean>(raw.ValidateConnection(c));

// ===== 元数据浏览 =====
export const serverInfo = (conn: string) => call<ServerInfo>(raw.ServerInfo(conn));
export const connectionCapabilities = (conn: string) =>
  call<Capability[]>(raw.ConnectionCapabilities(conn));
export const listDatabases = (conn: string) => call<DatabaseInfo[]>(raw.ListDatabases(conn));
export const listSchemas = (conn: string, database: string) =>
  call<string[]>(raw.ListSchemas(conn, database));
export const listObjects = (conn: string, database: string, schema: string) =>
  call<ObjectInfo[]>(raw.ListObjects(conn, database, schema));
export const describeObject = (conn: string, ref: ObjectRef) =>
  call<TableSchema>(raw.DescribeObject({ conn, ref }));

// ===== 查询执行 =====
export const executeQuery = async (req: QueryRequest) =>
  normalizeOutcome(await call<QueryOutcome>(raw.ExecuteQuery(req)));
export const explainQuery = async (req: QueryRequest) =>
  normalizeOutcome(await call<QueryOutcome>(raw.ExplainQuery(req)));
export const execStatement = (req: ExecRequest) => call<any>(raw.ExecStatement(req));
export const executeScript = async (req: ScriptRequest) =>
  (await call<QueryOutcome[]>(raw.ExecuteScript(req))).map(normalizeOutcome);

// ===== 结构/建表 =====
export const previewCreateDatabase = (conn: string, spec: DatabaseSpec) =>
  call<Script>(raw.PreviewCreateDatabase(conn, spec));
export const previewAlterDatabase = (conn: string, name: string, spec: DatabaseSpec) =>
  call<Script>(raw.PreviewAlterDatabase(conn, name, spec));
export const previewCreateTable = (conn: string, def: TableDefinition) =>
  call<Script>(raw.PreviewCreateTable(conn, def));
export const previewAlterTable = (conn: string, ref: ObjectRef, changes: SchemaChange[]) =>
  call<Script>(raw.PreviewAlterTable(conn, ref, changes));
export const previewDropObject = (conn: string, ref: ObjectRef) =>
  call<Script>(raw.PreviewDropObject(conn, ref));
export const previewRenameTable = (conn: string, ref: ObjectRef, newName: string) =>
  call<Script>(raw.PreviewRenameTable(conn, ref, newName));
export const showCreateObject = (conn: string, ref: ObjectRef) =>
  call<string>(raw.ShowCreateObject(conn, ref));
export const previewTableAction = (conn: string, ref: ObjectRef, action: string) =>
  call<Script>(raw.PreviewTableAction(conn, ref, action));
export const previewCopyTable = (conn: string, req: { ref: ObjectRef; new_name: string; with_data: boolean }) =>
  call<Script>(raw.PreviewCopyTable(conn, req));
export const applyScript = (conn: string, script: Script) =>
  call<{ outcomes: any[] }>(raw.ApplyScript(conn, script));

// ===== 数据编辑 =====
export const previewDataEdit = (conn: string, req: EditRequest) =>
  call<string[]>(raw.PreviewDataEdit(conn, req));
export const applyDataEdit = (conn: string, req: EditRequest) =>
  call<EditResult>(raw.ApplyDataEdit(conn, req));
export const tableKeyColumns = (conn: string, ref: ObjectRef) =>
  call<string[]>(raw.TableKeyColumns(conn, ref));

// ===== 导入 =====
export const importFile = (req: ImportRequest) => call<ImportOutcomeDTO>(raw.ImportFile(req));
export const exportDatabase = (conn: string, db: string, structure: boolean, data: boolean, maxRows: number) =>
  call<string>(raw.ExportDatabase(conn, db, structure, data, maxRows));

// ===== 历史 / 收藏 / 信息 =====
export const info = () => call<AppInfo>(raw.Info());

export const listHistory = (limit = 0) => call<any[]>(raw.ListHistory(limit));
export const clearHistory = () => call<boolean>(raw.ClearHistory());
export const listSavedQueries = () => call<any[]>(raw.ListSavedQueries());
export const saveSavedQuery = (q: any) => call<any>(raw.SaveSavedQuery(q));
export const deleteSavedQuery = (id: string) => call<boolean>(raw.DeleteSavedQuery(id));
export const listPreferences = () => call<any>(raw.ListPreferences());
export const savePreferences = (p: any) => call<boolean>(raw.SavePreferences(p));
