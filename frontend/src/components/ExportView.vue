<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { Cell, QueryOutcome, Row } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

type Format = "csv" | "tsv" | "json" | "sql" | "markdown" | "ndjson" | "es_bulk";
type FormatEntry = { value: Format; label: string; ext: string; hint: string };
const FORMATS: FormatEntry[] = [
  { value: "csv", label: "CSV", ext: "csv", hint: "逗号分隔，Excel 可直接打开" },
  { value: "tsv", label: "TSV", ext: "tsv", hint: "制表符分隔，适合粘贴到表格" },
  { value: "json", label: "JSON", ext: "json", hint: "对象数组，保留 null" },
  { value: "sql", label: "SQL INSERT", ext: "sql", hint: "可直接回放到同结构表" },
  { value: "markdown", label: "Markdown", ext: "md", hint: "文档表格" },
];
/** Elasticsearch 原生无损格式：基于原始 JSON 文档，而非采样列式投影。 */
const ES_FORMATS: FormatEntry[] = [
  { value: "ndjson", label: "NDJSON", ext: "ndjson", hint: "每行一个原始文档（含 _id），可直接用导入向导回导" },
  { value: "es_bulk", label: "ES Bulk", ext: "bulk.ndjson", hint: "_bulk 可回放 NDJSON：动作行 + _source 行" },
];

const connName = ref(props.tab.conn);
const dbName = ref(props.tab.database ?? "");
const tableName = ref(props.tab.table ?? "");
const source = ref<"table" | "sql" | "database">(props.tab.table ? "table" : props.tab.database ? "database" : "table");
const sql = ref("");
const maxRows = ref(5000);
const format = ref<Format>("csv");
const includeHeader = ref(true);
const nullText = ref("");
const sqlTarget = ref(props.tab.table ?? "");
const busy = ref(false);
const error = ref("");
const outcome = ref<QueryOutcome | null>(null);
/** data = 仅数据；structure_data = 表结构 + 数据。 */
const exportMode = ref<"data" | "structure_data">("data");
const ddl = ref("");

// 整个数据库导出相关状态
const dbStructure = ref(true);
const dbData = ref(true);
const dbContent = ref("");

// Redis 命令导出（按连接类型选语言）
const redisContent = ref("");

const isDatabase = computed(() => source.value === "database");
const databases = computed(() => store.dbCache[connName.value] || []);
const tables = computed(() =>
  dbName.value ? store.objectsCache[store.cacheKey(connName.value, dbName.value)] || [] : [],
);
const page = computed(() => outcome.value?.pages[0] ?? null);
const columns = computed(() => page.value?.columns ?? []);
const rows = computed<Row[]>(() => page.value?.rows ?? []);
// 连接类型感知：关系型 → sql；MongoDB → mongo_filter；ES → es_dsl；Redis → 命令导出（单独路径）。
const kind = computed(() => store.connections.find((c) => c.name === connName.value)?.kind ?? "");
const isES = computed(() => kind.value === "elasticsearch");
const isRelational = computed(() => !["mongodb", "redis", "elasticsearch"].includes(kind.value));
const redisMode = computed(() => kind.value === "redis" && !isDatabase.value);

const ext = computed(() => [...FORMATS, ...ES_FORMATS].find((f) => f.value === format.value)?.ext ?? "txt");
// 文档型没有 SQL 概念；ES 额外提供基于原始文档的 NDJSON / Bulk 无损格式。
const formats = computed<FormatEntry[]>(() => {
  if (isES.value) return [...FORMATS.filter((f) => f.value !== "sql"), ...ES_FORMATS];
  if (kind.value === "mongodb") return FORMATS.filter((f) => f.value !== "sql");
  return FORMATS;
});
const baseName = computed(() => sqlTarget.value.trim() || tableName.value.trim() || "export");
const fileName = computed(() => `${baseName.value}.${ext.value}`);
const schemaFileName = computed(() => `${baseName.value}.schema.sql`);
// 表结构（DDL）仅关系型有意义，文档/键值库没有 CREATE TABLE 概念。
const canStructure = computed(() => isRelational.value && source.value === "table" && !!tableName.value.trim());
const withStructure = computed(() => exportMode.value === "structure_data" && canStructure.value && !!ddl.value);

// Bulk 动作行的目标索引默认按「逻辑库 + . + 索引名」还原物理名，允许用户覆盖。
const defaultTargetIndex = computed(() => {
  const db = dbName.value.trim();
  const table = tableName.value.trim();
  if (!table) return "";
  if (!db || db === "default") return table;
  return table.startsWith(`${db}.`) ? table : `${db}.${table}`;
});
const targetIndex = ref("");
const bulkIndex = computed(() => targetIndex.value.trim() || defaultTargetIndex.value || "exported_index");
const sqlPlaceholder = computed(() => (isES.value
  ? '{"index":"users","query":{"match":{"status":"paid"}},"size":100}'
  : "SELECT id, name FROM users WHERE active = 1"));

// 切换连接时，非关系型强制回到「表/集合/键」来源，并清空结果。
watch(connName, () => {
  if (!isRelational.value && source.value !== "table") source.value = "table";
  // 切换数据库类型后，若旧格式不在新类型支持的列表中则回退到 CSV。
  if (!formats.value.some((f) => f.value === format.value)) format.value = "csv";
  dbContent.value = "";
  outcome.value = null;
  redisContent.value = "";
  // 确保数据库下拉框有可选项（与表名下拉框行为一致）。
  if (connName.value) store.loadDatabases(connName.value).catch(() => {});
}, { immediate: true });

watch([source, exportMode, tableName], () => { ddl.value = ""; });
watch(source, () => { dbContent.value = ""; });

function textOf(cell: Cell | undefined): string {
  if (!cell || cell.null) return nullText.value;
  return cell.text ?? "";
}

// ---- Redis 命令导出辅助 ----
function rowTexts(row: Cell[]): string[] {
  return row.map((c) => c?.text ?? "");
}
function valOf(row: Cell[]): string {
  const t = rowTexts(row);
  return t.length > 1 ? t[1] : (t[0] ?? "");
}
function q(s: string): string {
  return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}
async function redisQuery(text: string): Promise<QueryOutcome> {
  return api.executeQuery({ conn: connName.value, query: { language: "redis", text, database: dbName.value } });
}

async function loadDatabase() {
  error.value = "";
  if (!connName.value) { error.value = "请选择连接"; return; }
  if (!dbName.value.trim()) { error.value = "请选择或填写数据库"; return; }
  busy.value = true;
  try {
    const content = await api.exportDatabase(
      connName.value, dbName.value.trim(), dbStructure.value, dbData.value, maxRows.value,
    );
    dbContent.value = content;
    outcome.value = null;
    const lineCount = content.split("\n").length;
    store.setStatus(`已生成整库导出脚本（约 ${lineCount} 行）`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`生成失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

// 生成 Redis 键的原生命令（SET/HSET/RPUSH/SADD/ZADD + EXPIRE）。
async function buildRedisKeyCommands(key: string): Promise<string[]> {
  const typeRes = await redisQuery(`TYPE ${q(key)}`);
  const typ = valOf(typeRes.pages[0]?.rows?.[0] ?? []) || "none";
  const ttlRes = await redisQuery(`TTL ${q(key)}`);
  const ttl = Number(valOf(ttlRes.pages[0]?.rows?.[0] ?? [])) || -1;
  const cmds: string[] = [];
  if (typ === "string") {
    const r = await redisQuery(`GET ${q(key)}`);
    cmds.push(`SET ${q(key)} ${q(valOf(r.pages[0]?.rows?.[0] ?? []))}`);
  } else if (typ === "hash") {
    const r = await redisQuery(`HGETALL ${q(key)}`);
    const flat = (r.pages[0]?.rows ?? []).map(valOf);
    const parts: string[] = [];
    for (let i = 0; i + 1 < flat.length; i += 2) parts.push(`${q(flat[i])} ${q(flat[i + 1])}`);
    if (parts.length) cmds.push(`HSET ${q(key)} ${parts.join(" ")}`);
  } else if (typ === "list") {
    const r = await redisQuery(`LRANGE ${q(key)} 0 -1`);
    const items = (r.pages[0]?.rows ?? []).map(valOf);
    if (items.length) cmds.push(`RPUSH ${q(key)} ${items.map(q).join(" ")}`);
  } else if (typ === "set") {
    const r = await redisQuery(`SMEMBERS ${q(key)}`);
    const members = (r.pages[0]?.rows ?? []).map(valOf);
    if (members.length) cmds.push(`SADD ${q(key)} ${members.map(q).join(" ")}`);
  } else if (typ === "zset") {
    const r = await redisQuery(`ZRANGE ${q(key)} 0 -1 WITHSCORES`);
    const flat = (r.pages[0]?.rows ?? []).map(valOf);
    const parts: string[] = [];
    for (let i = 0; i + 1 < flat.length; i += 2) parts.push(`${q(flat[i + 1])} ${q(flat[i])}`); // score member
    if (parts.length) cmds.push(`ZADD ${q(key)} ${parts.join(" ")}`);
  } else {
    cmds.push(`# 跳过键 ${q(key)}：暂不支持导出的类型 ${typ}`);
  }
  if (ttl >= 0) cmds.push(`EXPIRE ${q(key)} ${ttl}`);
  return cmds;
}

async function loadRedisExport() {
  error.value = "";
  if (!connName.value) { error.value = "请选择连接"; return; }
  if (!dbName.value.trim()) { error.value = "请选择或填写数据库"; return; }
  busy.value = true;
  outcome.value = null;
  redisContent.value = "";
  try {
    const pattern = tableName.value.trim() || "*";
    const browse = await api.executeQuery({
      conn: connName.value,
      query: { language: "redis", ref: { database: dbName.value, name: "" }, filter: pattern, max_rows: maxRows.value },
    });
    const keyRows = browse.pages[0]?.rows ?? [];
    const cmds: string[] = [];
    for (const r of keyRows) {
      const key = r[0]?.text ?? "";
      if (!key) continue;
      try {
        cmds.push(...(await buildRedisKeyCommands(key)));
      } catch (e: any) {
        cmds.push(`# 跳过键 ${q(key)}（读取失败：${e?.message ?? e}）`);
      }
    }
    redisContent.value = cmds.join("\n");
    store.setStatus(`已生成 ${cmds.length} 条 Redis 命令（${keyRows.length} 个键）`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`生成失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

function exportLanguage(): string {
  if (kind.value === "mongodb") return "mongo_filter";
  if (isES.value) return "es_dsl";
  return "sql";
}

async function load() {
  if (isDatabase.value) { await loadDatabase(); return; }
  if (redisMode.value) { await loadRedisExport(); return; }
  error.value = "";
  if (!connName.value) { error.value = "请选择连接"; return; }
  if (source.value === "table" && !tableName.value.trim()) { error.value = "请选择或填写要导出的表"; return; }
  if (source.value === "sql" && !sql.value.trim()) { error.value = "请填写导出用的 SQL"; return; }
  busy.value = true;
  outcome.value = null;
  try {
    outcome.value = await api.executeQuery({
      conn: connName.value,
      query: source.value === "table"
        ? { language: exportLanguage(), ref: { database: dbName.value, name: tableName.value.trim() }, max_rows: maxRows.value }
        : { language: exportLanguage(), text: sql.value.trim(), database: dbName.value || undefined, max_rows: maxRows.value },
    });
    if (!sqlTarget.value) sqlTarget.value = tableName.value.trim();
    ddl.value = "";
    if (exportMode.value === "structure_data" && source.value === "table" && isRelational.value) {
      try {
        ddl.value = await api.showCreateObject(connName.value, {
          database: dbName.value,
          name: tableName.value.trim(),
        });
      } catch (e: any) {
        store.setStatus(`无法读取表结构，将只导出数据：${e?.message ?? e}`, "warn");
      }
    }
    store.setStatus(
      `已读取 ${rows.value.length} 行用于导出${ddl.value ? "（含建表语句）" : ""}`,
      "ok",
    );
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`读取失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

function quote(value: string, delimiter: string): string {
  if (value.includes('"') || value.includes(delimiter) || value.includes("\n") || value.includes("\r")) {
    return `"${value.replace(/"/g, '""')}"`;
  }
  return value;
}
function sqlValue(cell: Cell | undefined): string {
  if (!cell || cell.null) return "NULL";
  return `'${(cell.text ?? "").replace(/'/g, "''")}'`;
}

function buildContent(): string {
  const cols = columns.value.map((c) => c.name);
  const data = rows.value;
  // ES 原生格式基于原始 JSON 文档（json_documents），完整保留嵌套结构，可与导入闭环。
  if (format.value === "ndjson" || format.value === "es_bulk") {
    const docs = page.value?.json_documents ?? [];
    const lines: string[] = [];
    for (const text of docs) {
      if (format.value === "ndjson") {
        lines.push(text);
        continue;
      }
      let doc: Record<string, any> = {};
      try { doc = JSON.parse(text) as Record<string, any>; } catch { doc = { _raw: text }; }
      const id = doc._id;
      const action: Record<string, any> = { _index: bulkIndex.value };
      if (id !== undefined && id !== null) {
        action._id = typeof id === "object" ? JSON.stringify(id) : String(id);
      }
      lines.push(JSON.stringify({ index: action }));
      delete doc._id;
      lines.push(JSON.stringify(doc));
    }
    return lines.join("\n");
  }
  if (format.value === "json") {
    return JSON.stringify(
      data.map((r) => {
        const obj: Record<string, string | null> = {};
        cols.forEach((c, i) => { obj[c] = r[i]?.null ? null : (r[i]?.text ?? ""); });
        return obj;
      }),
      null,
      2,
    );
  }
  if (format.value === "sql") {
    const target = sqlTarget.value.trim() || tableName.value.trim() || "exported_table";
    const colList = cols.map((c) => `\`${c}\``).join(", ");
    return data
      .map((r) => `INSERT INTO \`${target}\` (${colList}) VALUES (${r.map(sqlValue).join(", ")});`)
      .join("\n");
  }
  if (format.value === "markdown") {
    const escape = (s: string) => s.replace(/\|/g, "\\|").replace(/\n/g, " ");
    const head = `| ${cols.map(escape).join(" | ")} |`;
    const sep = `| ${cols.map(() => "---").join(" | ")} |`;
    const body = data.map((r) => `| ${r.map((c) => escape(textOf(c))).join(" | ")} |`);
    return [head, sep, ...body].join("\n");
  }
  const delimiter = format.value === "tsv" ? "\t" : ",";
  const lines: string[] = [];
  if (includeHeader.value) lines.push(cols.map((c) => quote(c, delimiter)).join(delimiter));
  for (const r of data) lines.push(r.map((c) => quote(textOf(c), delimiter)).join(delimiter));
  return lines.join("\r\n");
}

/** SQL 格式把 CREATE TABLE 合并进同一个文件；其他格式单独产出 .schema.sql。 */
function mainContent(): string {
  if (isDatabase.value) return dbContent.value;
  if (redisMode.value) return redisContent.value;
  if (format.value === "sql" && withStructure.value) {
    return `-- 表结构：${tableName.value.trim()}\n${ddl.value.trim()}\n\n` + buildContent();
  }
  return buildContent();
}
function schemaContent(): string {
  return `-- 表结构：${tableName.value.trim()}\n${ddl.value.trim()}\n`;
}
function saveFile(content: string, name: string) {
  const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function download() {
  if (isDatabase.value) {
    if (!dbContent.value) { await loadDatabase(); if (!dbContent.value) return; }
    const name = `${dbName.value.trim() || "database"}.sql`;
    saveFile(dbContent.value, name);
    store.setStatus(`已导出数据库 ${dbName.value} → ${name}`, "ok");
    return;
  }
  if (redisMode.value) {
    if (!redisContent.value) { await loadRedisExport(); if (!redisContent.value) return; }
    const name = `${dbName.value.trim() || "redis"}.redis`;
    saveFile(redisContent.value, name);
    const n = redisContent.value.split("\n").filter(Boolean).length;
    store.setStatus(`已导出 ${n} 条 Redis 命令 → ${name}`, "ok");
    return;
  }
  if (!outcome.value) { await load(); if (!outcome.value) return; }
  const bom = format.value === "csv" || format.value === "tsv" ? "\uFEFF" : "";
  saveFile(bom + mainContent(), fileName.value);
  const extra = withStructure.value && format.value !== "sql";
  if (extra) {
    window.setTimeout(() => saveFile(schemaContent(), schemaFileName.value), 400);
  }
  store.setStatus(
    `已导出 ${rows.value.length} 行 → ${fileName.value}${extra ? ` + ${schemaFileName.value}` : ""}`,
    "ok",
  );
}

async function copy() {
  if (isDatabase.value) {
    if (!dbContent.value) { await loadDatabase(); if (!dbContent.value) return; }
    try {
      await navigator.clipboard.writeText(dbContent.value);
      store.notify("整库导出脚本已复制到剪贴板", "ok", 1800);
    } catch {
      store.setStatus("剪贴板不可用", "warn");
    }
    return;
  }
  if (redisMode.value) {
    if (!redisContent.value) { await loadRedisExport(); if (!redisContent.value) return; }
    try {
      await navigator.clipboard.writeText(redisContent.value);
      store.notify("Redis 命令已复制到剪贴板", "ok", 1800);
    } catch {
      store.setStatus("剪贴板不可用", "warn");
    }
    return;
  }
  if (!outcome.value) { await load(); if (!outcome.value) return; }
  try {
    await navigator.clipboard.writeText(mainContent());
    store.notify("导出内容已复制到剪贴板", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}
</script>

<template>
  <div class="export-page">
    <div class="toolbar page-head">
      <Icon name="download" :size="15" />
      <strong>导出数据</strong>
      <span class="badge">{{ connName }}</span>
      <span v-if="dbName" class="badge">{{ dbName }}</span>
      <span class="grow"></span>
      <span v-if="!isDatabase && outcome" class="badge badge-ok">{{ rows.length }} 行 · {{ columns.length }} 列</span>
      <span v-else-if="redisMode && redisContent" class="badge badge-ok">{{ redisContent.split('\n').filter(Boolean).length }} 条命令</span>
    </div>

    <div class="page-body scroll">
      <div class="card">
        <div class="card-head"><Icon name="database" :size="14" /><h4>1 · 数据来源</h4></div>
        <div class="card-body col" style="gap: 10px">
          <div class="form-grid three">
            <div class="field">
              <label class="field-label">连接 *</label>
              <select v-model="connName"><option v-for="c in store.connections" :key="c.name" :value="c.name">{{ c.name }}</option></select>
            </div>
            <div class="field">
              <label class="field-label">数据库</label>
              <select v-model="dbName">
                <option value="">请选择数据库</option>
                <option v-for="d in databases" :key="d.name" :value="d.name">{{ d.name }}</option>
              </select>
            </div>
            <div class="field">
              <label class="field-label">每表最多行数</label>
              <input v-model.number="maxRows" type="number" min="1" max="500000" />
            </div>
          </div>
          <div class="row" style="gap: 14px">
            <template v-if="isRelational">
              <label class="check-line"><input type="radio" value="table" v-model="source" /> 导出表数据</label>
              <label class="check-line"><input type="radio" value="sql" v-model="source" /> 使用自定义 SQL</label>
              <label class="check-line"><input type="radio" value="database" v-model="source" /> 整个数据库</label>
            </template>
            <template v-else-if="kind === 'elasticsearch'">
              <label class="check-line"><input type="radio" value="table" v-model="source" /> 导出索引数据</label>
              <label class="check-line"><input type="radio" value="sql" v-model="source" /> 使用自定义 DSL</label>
              <span class="field-hint">NDJSON / ES Bulk 导出原始 JSON 文档，完整保留嵌套结构；CSV / JSON 等基于采样字段展开。</span>
            </template>
            <template v-else-if="kind === 'mongodb'">
              <label class="check-line"><input type="radio" value="table" v-model="source" /> 导出集合数据</label>
              <span class="field-hint">MongoDB 集合以 JSON 文档形式导出（CSV / JSON / SQL 等格式基于采样字段展开，每行一个文档）。</span>
            </template>
            <template v-else-if="kind === 'redis'">
              <label class="check-line"><input type="radio" value="table" v-model="source" /> 导出键（命令）</label>
              <span class="field-hint">Redis 以可回放原生命令（SET / HSET / RPUSH / SADD / ZADD + EXPIRE）导出匹配键。</span>
            </template>
          </div>
          <div v-if="source === 'table' && !redisMode" class="field">
            <label class="field-label">{{ isRelational ? '表名 *' : isES ? '索引名 *' : '集合名 *' }}</label>
            <select v-model="tableName">
              <option value="">{{ dbName ? `选择${isRelational ? '表' : isES ? '索引' : '集合'}…` : '请先选择数据库' }}</option>
              <option v-for="t in tables" :key="t.name" :value="t.name">{{ t.name }}</option>
            </select>
          </div>
          <div v-else-if="source === 'table' && redisMode" class="field">
            <label class="field-label">键模式 (SCAN MATCH)</label>
            <input v-model="tableName" placeholder="* 匹配全部，如 user:*" />
          </div>
          <div v-else-if="source === 'sql'" class="field">
            <label class="field-label">{{ isES ? '查询 DSL *' : 'SQL 语句 *' }}</label>
            <textarea
              v-model="sql"
              class="sql-input mono"
              rows="4"
              :placeholder="sqlPlaceholder"
            ></textarea>
            <span v-if="isES" class="field-hint">DSL 为 _search 请求体；可带 <code>index</code> 指定索引。size 会按「每表最多行数」限制。</span>
          </div>
          <div v-else class="field-hint">
            将导出该数据库下所有表（结构与 / 或数据）。生成的是可回放的 SQL 脚本，请在下方选择导出内容后点击「生成并下载」。
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-head"><Icon name="settings" :size="14" /><h4>2 · 导出内容与格式</h4></div>
        <div class="card-body col" style="gap: 10px">
          <template v-if="isDatabase">
            <div class="row wrap" style="gap: 14px">
              <label class="check-line"><input type="checkbox" v-model="dbStructure" /> 导出表结构（CREATE TABLE）</label>
              <label class="check-line"><input type="checkbox" v-model="dbData" /> 导出数据（INSERT）</label>
            </div>
            <span v-if="!dbStructure && !dbData" class="field-hint warn">请至少选择结构或数据之一。</span>
            <span class="field-hint">整库导出固定生成 SQL 脚本（包含所有表的建表语句与 INSERT）。每表最多取「每表最多行数」行。</span>
          </template>
          <template v-else>
            <div v-if="isRelational" class="row wrap" style="gap: 14px">
              <label class="check-line"><input type="radio" value="data" v-model="exportMode" /> 仅导出数据</label>
              <label class="check-line">
                <input type="radio" value="structure_data" v-model="exportMode" :disabled="!canStructure" />
                表结构 + 数据
              </label>
              <span v-if="!canStructure" class="field-hint">只有关系型「导出表数据」来源才能附带表结构。</span>
            </div>
            <div v-else class="row wrap" style="gap: 14px">
              <span class="field-hint">{{ isES
                ? 'Elasticsearch 索引以 JSON 文档形式导出；NDJSON / ES Bulk 保留完整嵌套结构，可无损回导。'
                : kind === 'mongodb' ? 'MongoDB 集合以文档形式导出，仅包含数据。' : '该类型仅支持导出数据。' }}</span>
            </div>
            <span v-if="exportMode === 'structure_data'" class="field-hint">
              SQL 格式会把 CREATE TABLE 与 INSERT 合并为一个文件；其他格式会额外下载 {{ schemaFileName }}。
            </span>
            <template v-if="!redisMode">
              <div class="row wrap" style="gap: 6px">
                <button
                  v-for="f in formats"
                  :key="f.value"
                  class="btn btn-sm"
                  :class="{ active: format === f.value }"
                  :title="f.hint"
                  @click="format = f.value"
                >{{ f.label }}</button>
              </div>
              <span class="field-hint">{{ formats.find((f) => f.value === format)?.hint }}</span>
              <div class="form-grid three">
                <div v-if="format === 'csv' || format === 'tsv'" class="field">
                  <label class="check-line"><input type="checkbox" v-model="includeHeader" /> 包含列名首行</label>
                </div>
                <div v-if="!['sql', 'ndjson', 'es_bulk'].includes(format)" class="field">
                  <label class="field-label">NULL 显示为</label>
                  <input v-model="nullText" placeholder="留空 = 空字符串" />
                </div>
                <div v-if="format === 'sql'" class="field">
                  <label class="field-label">INSERT 目标表</label>
                  <input v-model="sqlTarget" placeholder="与源表同名" />
                </div>
                <div v-if="format === 'es_bulk'" class="field">
                  <label class="field-label">Bulk 目标索引</label>
                  <input v-model="targetIndex" :placeholder="defaultTargetIndex || '物理索引名'" />
                  <span class="field-hint">默认 {{ defaultTargetIndex || '按所选索引自动推算' }}，可改成任意目标索引。</span>
                </div>
              </div>
            </template>
            <span v-else class="field-hint">导出为 Redis 命令脚本（.redis），每行一条命令，可直接在 redis-cli 中回放。</span>
          </template>
        </div>
      </div>

      <div class="card">
        <div class="card-head">
          <Icon name="eye" :size="14" /><h4>3 · 预览</h4>
          <span class="grow"></span>
          <button class="btn btn-sm" :disabled="busy" @click="load">
            <span v-if="busy" class="spinner" style="width: 12px; height: 12px"></span>
            <Icon v-else name="refresh" :size="12" /> {{ isDatabase ? "生成导出" : "读取数据" }}
          </button>
        </div>
        <div class="card-body">
          <EmptyState
            v-if="!isDatabase && !redisMode && !outcome"
            :compact="true"
            icon="table"
            title="尚未读取数据"
            description="设置数据来源后点击「读取数据」，确认内容无误再导出。"
          />
          <EmptyState
            v-else-if="redisMode && !redisContent"
            :compact="true"
            icon="key"
            title="尚未生成命令"
            description="设置键模式后点击「读取数据」，将生成可回放的 Redis 命令脚本。"
          />
          <EmptyState
            v-else-if="isDatabase && !dbContent"
            :compact="true"
            icon="database"
            title="尚未生成整库脚本"
            description="选择导出内容后点击「生成导出」，预览完整 SQL 脚本。"
          />
          <div v-else-if="isDatabase" class="scroll preview-scroll">
            <pre class="code">{{ dbContent }}</pre>
          </div>
          <div v-else-if="redisMode" class="scroll preview-scroll">
            <pre class="code">{{ redisContent }}</pre>
          </div>
          <template v-else>
            <div class="scroll preview-scroll">
              <table class="data-table">
                <thead><tr><th class="rownum">#</th><th v-for="c in columns" :key="c.name">{{ c.name }}</th></tr></thead>
                <tbody>
                  <tr v-for="(r, i) in rows.slice(0, 50)" :key="i">
                    <td class="rownum dim">{{ i + 1 }}</td>
                    <td v-for="(c, ci) in columns" :key="ci" class="cell" :class="{ null: r[ci]?.null }">{{ textOf(r[ci]) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <span v-if="outcome && rows.length > 50" class="field-hint">仅预览前 50 行，导出包含全部 {{ rows.length }} 行。</span>
            <div v-if="ddl" class="ddl-preview">
              <div class="row" style="gap: 6px; margin-bottom: 6px">
                <Icon name="code" :size="13" />
                <strong style="font-size: 11px">表结构（{{ withStructure ? "随本次导出写出" : "仅供参考" }}）</strong>
                <span class="grow"></span>
                <span class="badge badge-info">{{ schemaFileName }}</span>
              </div>
              <pre class="code">{{ ddl }}</pre>
            </div>
          </template>
        </div>
      </div>

      <div v-if="error" class="alert alert-error"><Icon name="alert" :size="14" /><span class="grow">{{ error }}</span></div>
    </div>

    <div class="toolbar page-foot">
      <span class="faint" style="font-size: 11px">
        <template v-if="isDatabase">{{ dbName || "database" }}.sql</template>
        <template v-else-if="redisMode">{{ dbName || "redis" }}.redis（Redis 命令）</template>
        <template v-else>
          {{ exportMode === "structure_data" ? "结构 + 数据" : "仅数据" }} · {{ fileName }}<span v-if="withStructure && format !== 'sql'"> + {{ schemaFileName }}</span>
        </template>
      </span>
      <span class="grow"></span>
      <button class="btn" :disabled="(isDatabase ? !dbContent : redisMode ? !redisContent : !outcome) || busy" @click="copy"><Icon name="copy" :size="13" /> 复制内容</button>
      <button class="btn btn-primary" :disabled="busy" @click="download"><Icon name="download" :size="13" /> 生成并下载</button>
    </div>
  </div>
</template>

<style scoped>
.export-page { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; overflow: hidden; }
.page-head, .page-foot { flex-shrink: 0; }
.page-body { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 10px; padding: 10px; background: var(--bg); }
.page-body > .card { flex-shrink: 0; }
.sql-input { width: 100%; resize: vertical; }
.preview-scroll { max-height: 360px; border: 1px solid var(--border); border-radius: var(--radius-sm); min-width: 0; white-space: pre-wrap; }
.cell { max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ddl-preview { margin-top: 10px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 8px 10px; background: var(--surface-2); min-width: 0; }
.ddl-preview pre { max-height: 160px; overflow: auto; white-space: pre-wrap; margin: 0; font-size: 10px; line-height: 1.6; }
.field-hint.warn { color: var(--warn); }
</style>
