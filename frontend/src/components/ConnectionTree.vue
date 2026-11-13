<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useWorkspace } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ConnectionConfig, ObjectInfo, Script } from "@/types/domain";
import type { MenuItem } from "@/types/ui";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";
import ContextMenu from "./ui/ContextMenu.vue";

const store = useWorkspace();
const emit = defineEmits<{
  (e: "edit-conn", c: ConnectionConfig | null): void;
}>();

const expandedConns = ref<Record<string, boolean>>({});
const expandedDbs = ref<Record<string, boolean>>({});
const menu = ref({ open: false, x: 0, y: 0, items: [] as MenuItem[] });
const loadingConns = ref<Record<string, boolean>>({});
// 当前选中的树节点标识：连接 c:name / 库 d:conn|db / 对象 o:conn|db|name。
// 单击与右键都会更新它，保证「选中资源」高亮在右键菜单出现时不会消失。
const selectedKey = ref("");

const q = computed(() => store.treeQuery.trim().toLowerCase());

onMounted(() => {
  store.loadConnections().catch((e: any) => store.setStatus(e?.message ?? "加载连接失败", "error"));
  store.loadOpenConns().catch(() => {});
});

// 搜索时按需补齐库/对象缓存（带防抖与数量上限，避免元数据风暴）。
let searchTimer: number | undefined;
watch(q, (value) => {
  if (searchTimer) window.clearTimeout(searchTimer);
  if (!value) return;
  searchTimer = window.setTimeout(async () => {
    let budget = 12;
    for (const c of store.connections) {
      try {
        const dbs = await store.loadDatabases(c.name);
        for (const db of dbs) {
          if (budget <= 0) break;
          budget -= 1;
          // Redis 键规模可能极大，搜索时也不在树中加载全部键。
          if (connKind(c.name) === "redis") continue;
          try {
            await store.loadObjects(c.name, db.name);
          } catch {
            /* 单个库不可用不阻断搜索 */
          }
        }
      } catch {
        /* 连接不可用 */
      }
    }
  }, 320);
});

function connOpen(name: string) {
  return q.value ? true : !!expandedConns.value[name];
}
function dbOpen(conn: string, db: string) {
  if (!q.value) return !!expandedDbs.value[`${conn}|${db}`];
  return !!store.objectsCache[store.cacheKey(conn, db)];
}
function visibleConns() {
  if (!q.value) return store.connections;
  return store.connections.filter(
    (c) =>
      c.name.toLowerCase().includes(q.value) ||
      c.kind.includes(q.value) ||
      (store.dbCache[c.name] || []).some((d) => d.name.toLowerCase().includes(q.value)),
  );
}
function visibleDbs(conn: string) {
  const dbs = store.dbCache[conn] || [];
  if (!q.value) return dbs;
  return dbs.filter(
    (d) => d.name.toLowerCase().includes(q.value) || hasMatchingObject(conn, d.name),
  );
}
function hasMatchingObject(conn: string, db: string) {
  if (!q.value) return false;
  return (store.objectsCache[store.cacheKey(conn, db)] || []).some((o) =>
    o.name.toLowerCase().includes(q.value),
  );
}
function visibleObjs(conn: string, db: string): ObjectInfo[] {
  const objs = store.objectsCache[store.cacheKey(conn, db)] || [];
  if (!q.value) return objs;
  return objs.filter((o) => o.name.toLowerCase().includes(q.value));
}

async function toggleConn(c: ConnectionConfig) {
  selectedKey.value = `c:${c.name}`;
  if (q.value) return;
  const open = !expandedConns.value[c.name];
  expandedConns.value[c.name] = open;
  store.activate(c.name);
  if (!open) return;
  loadingConns.value[c.name] = true;
  try {
    await store.loadDatabases(c.name);
  } catch (e: any) {
    store.setStatus(e?.message ?? "加载数据库失败", "error");
  } finally {
    loadingConns.value[c.name] = false;
    store.loadOpenConns().catch(() => {});
  }
}

async function toggleDb(conn: string, db: string) {
  selectedKey.value = `d:${conn}|${db}`;
  const key = `${conn}|${db}`;
  if (q.value) return;
  const open = !expandedDbs.value[key];
  expandedDbs.value[key] = open;
  store.activeDb = db;
  if (!open) return;
  // Redis 的键可能上千万，不在树中展开加载（避免阻塞服务端），改由键搜索页浏览。
  if (connKind(conn) === "redis") return;
  try {
    await store.loadObjects(conn, db);
  } catch (e: any) {
    store.setStatus(e?.message ?? "加载对象失败", "error");
  }
}

// 双击数据库：Redis 打开键搜索页；其它类型沿用展开行为。
function onDbDblClick(conn: string, db: string) {
  if (connKind(conn) === "redis") {
    store.openRedisKeysTab(conn, db);
    return;
  }
  toggleDb(conn, db);
}

function collapseAll() {
  expandedConns.value = {};
  expandedDbs.value = {};
}
async function refreshAll() {
  store.invalidateAll();
  await store.loadConnections();
  store.setStatus("连接与元数据已刷新", "ok");
}
async function refreshConn(name: string) {
  delete store.dbCache[name];
  delete store.objectsCache[name];
  for (const k of Object.keys(store.objectsCache)) {
    if (k.startsWith(`${name}|`)) delete store.objectsCache[k];
  }
  try {
    await store.loadDatabases(name);
    store.setStatus(`已刷新 ${name}`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "刷新失败", "error");
  }
  store.loadOpenConns().catch(() => {});
}
async function refreshObjects(conn: string, db: string) {
  store.invalidateObjects(conn, db);
  try {
    await store.loadObjects(conn, db);
    store.setStatus(`已刷新 ${db} 的对象`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "刷新失败", "error");
  }
}

function firstDb(conn: string) {
  return (store.dbCache[conn] || [])[0]?.name ?? store.activeDb;
}
function objRef(db: string, name: string) {
  return { database: db, name };
}

async function testConn(c: ConnectionConfig) {
  store.setStatus(`正在测试 ${c.name} …`);
  try {
    const info = await api.testSavedConnection(c.name);
    store.connStatus[c.name] = "on";
    store.setStatus(`${c.name} 连接正常 · ${info.kind} ${info.version}`, "ok");
  } catch (e: any) {
    store.connStatus[c.name] = "err";
    store.setStatus(`${c.name} 连接失败：${e?.message ?? e}`, "error");
  }
}

async function removeConn(c: ConnectionConfig) {
  const ok = await store.confirm({
    title: "删除连接",
    message: `将永久删除连接「${c.name}」及其保存的凭证。此操作不可撤销。`,
    danger: true,
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await api.removeConnection(c.name);
    for (const t of store.tabs.filter((x) => x.conn === c.name)) store.closeTab(t.id);
    store.invalidateAll();
    await store.loadConnections();
    store.setStatus(`已删除连接 ${c.name}`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "删除失败", "error");
  }
}

async function copyName(name: string) {
  try {
    await navigator.clipboard.writeText(name);
    store.notify(`已复制：${name}`, "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

function connKind(conn: string) {
  return store.connections.find((c) => c.name === conn)?.kind ?? "mysql";
}
function quoteIdent(name: string, kind: string) {
  if (kind === "sqlserver") return `[${name.replace(/]/g, "]]")}]`;
  if (kind === "postgresql" || kind === "oracle" || kind === "sqlite") {
    return `"${name.replace(/"/g, '""')}"`;
  }
  return `\`${name.replace(/`/g, "``")}\``;
}
function relational(kind: string) {
  return !["mongodb", "redis"].includes(kind);
}

async function copyTable(conn: string, db: string, o: ObjectInfo, withData: boolean) {
  const ref = objRef(db, o.name);
  const newName = await store.promptText({
    title: withData ? `复制表结构和数据 · ${o.name}` : `复制表结构 · ${o.name}`,
    message: `将创建新表于数据库 ${db}。请输入新表名：`,
    label: "新表名",
    value: `${o.name}_copy`,
    confirmText: "预览脚本",
  });
  if (!newName || newName.trim() === o.name) return;
  try {
    const script = await api.previewCopyTable(conn, { ref, new_name: newName.trim(), with_data: withData });
    const done = await applyScriptWithConfirm(conn, script, {
      title: `复制表 ${withData ? "结构与数据" : "结构"} · ${o.name}`,
      confirmText: "复制",
      // 复制数据时不展示 SQL：数据量大，机械的 INSERT...SELECT 预览无意义。
      hideSQL: withData,
    });
    if (done) await refreshObjects(conn, db);
  } catch (e: any) {
    store.setStatus(`复制表失败：${e?.message ?? e}`, "error");
  }
}

/** 统一的「脚本预览 → 二次确认 → 执行」流程。 */
async function applyScriptWithConfirm(
  conn: string,
  script: Script,
  opts: { title: string; confirmText?: string; danger?: boolean; requireText?: string; subject?: string; hideSQL?: boolean },
) {
  // hideSQL：复制「结构+数据」时数据量可能很大，机械的 INSERT...SELECT 预览无意义，
  // 仅展示注意事项而不展示 SQL 文本。
  const message = opts.hideSQL
    ? script.warnings?.length
      ? `注意：${script.warnings.join("；")}`
      : "确认执行该操作？"
    : `将在 ${conn} 执行：\n${script.statements.join("\n")}${
        script.warnings?.length ? `\n\n注意：${script.warnings.join("；")}` : ""
      }`;
  const ok = await store.confirm({
    title: opts.title,
    message,
    confirmText: opts.confirmText ?? "执行",
    danger: opts.danger,
    requireText: opts.requireText,
    subject: opts.subject,
  });
  if (!ok) return false;
  try {
    await api.applyScript(conn, script);
    store.setStatus(`${opts.title} 完成`, "ok");
    return true;
  } catch (e: any) {
    store.setStatus(`${opts.title} 失败：${e?.message ?? e}`, "error");
    return false;
  }
}

async function renameObject(conn: string, db: string, o: ObjectInfo) {
  const ref = objRef(db, o.name);
  const name = await store.promptText({
    title: `重命名 ${o.name}`,
    message: "输入新的对象名称（不含库名），会先生成脚本供确认。",
    label: "新名称",
    value: o.name,
    confirmText: "预览脚本",
  });
  if (!name || name.trim() === o.name) return;
  try {
    const script = await api.previewRenameTable(conn, ref, name.trim());
    const done = await applyScriptWithConfirm(conn, script, {
      title: `重命名 ${o.name}`, confirmText: "重命名", danger: true,
    });
    if (!done) return;
    store.renameObjectTabs(conn, ref, name.trim());
    await refreshObjects(conn, db);
  } catch (e: any) {
    store.setStatus(`重命名失败：${e?.message ?? e}`, "error");
  }
}

async function dropObject(conn: string, db: string, o: ObjectInfo) {
  const ref = objRef(db, o.name);
  try {
    const script = await api.previewDropObject(conn, ref);
    const done = await applyScriptWithConfirm(conn, script, {
      title: `删除 ${o.name}`, confirmText: "删除", danger: true, subject: o.name,
    });
    if (!done) return;
    for (const t of store.tabs.filter(
      (x) => x.conn === conn && x.ref?.database === db && x.ref.name === o.name,
    )) store.closeTab(t.id);
    await refreshObjects(conn, db);
  } catch (e: any) {
    store.setStatus(`删除失败：${e?.message ?? e}`, "error");
  }
}

async function alterDatabase(conn: string, db: string) {
  const value = await store.promptText({
    title: `修改数据库 ${db}`,
    message: "格式：字符集/排序规则，例如 utf8mb4/utf8mb4_general_ci；只填字符集也可以。",
    label: "字符集 / 排序规则",
    value: "utf8mb4",
    confirmText: "预览脚本",
  });
  if (!value) return;
  const [cs, co] = value.split("/").map((s) => s.trim());
  try {
    const script = await api.previewAlterDatabase(conn, db, {
      name: db, charset: cs || undefined, collation: co || undefined,
    });
    if (await applyScriptWithConfirm(conn, script, { title: `修改数据库 ${db}`, danger: true })) {
      await refreshConn(conn);
    }
  } catch (e: any) {
    store.setStatus(`修改失败：${e?.message ?? e}`, "error");
  }
}

async function renameDatabase(conn: string, db: string) {
  const name = await store.promptText({
    title: `重命名数据库 ${db}`,
    message: "输入新库名；部分数据库类型不支持直接重命名，会改为打开 SQL 编辑器。",
    label: "新库名",
    value: db,
    confirmText: "预览脚本",
  });
  if (!name || name.trim() === db) return;
  const kind = connKind(conn);
  let stmt = "";
  if (kind === "postgresql") {
    stmt = `ALTER DATABASE ${quoteIdent(db, kind)} RENAME TO ${quoteIdent(name.trim(), kind)};`;
  } else if (kind === "sqlserver") {
    stmt = `ALTER DATABASE ${quoteIdent(db, kind)} MODIFY NAME = ${quoteIdent(name.trim(), kind)};`;
  } else {
    store.notify(`${kind} 没有统一的重命名数据库语句，已打开 SQL 编辑器供手动处理`, "warn", 6000);
    store.openSQLTab(
      conn,
      `-- ${db} -> ${name.trim()}\n-- 该数据库类型不支持直接重命名，请按目标库语法手动执行。`,
    );
    return;
  }
  const done = await applyScriptWithConfirm(conn, { statements: [stmt] }, {
    title: `重命名数据库 ${db}`, confirmText: "重命名", danger: true, requireText: db,
  });
  if (done) await refreshConn(conn);
}

async function dropDatabase(conn: string, db: string) {
  const kind = connKind(conn);
  if (!["mysql", "postgresql", "sqlserver"].includes(kind)) {
    store.notify(`${kind} 不支持通过 SQL 删除数据库`, "warn", 6000);
    return;
  }
  const stmt = `DROP DATABASE ${quoteIdent(db, kind)};`;
  const done = await applyScriptWithConfirm(conn, {
    statements: [stmt],
    warnings: ["删除数据库会丢失其中全部对象与数据，不可撤销。"],
  }, {
    title: `删除数据库 ${db}`, confirmText: "删除", danger: true, requireText: db,
  });
  if (!done) return;
  for (const t of store.tabs.filter((x) => x.conn === conn && (x.ref?.database === db || x.database === db))) {
    store.closeTab(t.id);
  }
  await refreshConn(conn);
}

/** 新增数据库：名称 + 可选字符集 → 方言脚本预览 → 确认执行。 */
async function createDatabase(conn: string) {
  const name = await store.promptText({
    title: "新增数据库",
    message: "输入新数据库名称，下一步可指定字符集与排序规则。",
    label: "数据库名",
    value: "",
    confirmText: "下一步",
  });
  if (!name) return;
  const opts = await store.promptText({
    title: `新增数据库 ${name.trim()}`,
    message: "格式：字符集/排序规则，例如 utf8mb4/utf8mb4_general_ci；填「默认」表示由数据库决定。",
    label: "字符集 / 排序规则",
    value: "默认",
    confirmText: "预览脚本",
  });
  if (!opts) return;
  const [cs, co] = opts.trim() === "默认" ? [] : opts.split("/").map((s) => s.trim());
  try {
    const script = await api.previewCreateDatabase(conn, {
      name: name.trim(),
      charset: cs || undefined,
      collation: co || undefined,
      if_not_exists: true,
    });
    const done = await applyScriptWithConfirm(conn, script, {
      title: `新增数据库 ${name.trim()}`, confirmText: "创建",
    });
    if (done) await refreshConn(conn);
  } catch (e: any) {
    store.setStatus(`新增数据库失败：${e?.message ?? e}`, "error");
  }
}

/** TRUNCATE / DELETE FROM：从表右键菜单触发，先预览脚本再确认。 */
async function runTableAction(conn: string, db: string, o: ObjectInfo, action: "truncate" | "empty") {
  const ref = objRef(db, o.name);
  const label = action === "truncate" ? "截断数据 (TRUNCATE)" : "清空数据 (DELETE)";
  try {
    const preview = await api.previewTableAction(conn, ref, action);
    const script: Script = {
      ...preview,
      warnings: [
        ...(preview.warnings ?? []),
        `将移除 ${o.name} 中的全部数据，表结构保留；此操作不可回滚。`,
      ],
    };
    const done = await applyScriptWithConfirm(conn, script, {
      title: `${label} · ${o.name}`, confirmText: label, danger: true, subject: o.name,
    });
    if (done) await refreshObjects(conn, db);
  } catch (e: any) {
    store.setStatus(`${label}失败：${e?.message ?? e}`, "error");
  }
}

function openMenu(e: MouseEvent, items: MenuItem[]) {
  menu.value = { open: true, x: e.clientX, y: e.clientY, items };
}

async function menuConn(e: MouseEvent, c: ConnectionConfig) {
  selectedKey.value = `c:${c.name}`;
  const db = firstDb(c.name);
  await store.loadOpenConns().catch(() => {});
  const kind = c.kind;
  const items: MenuItem[] = [
    { label: "新建查询", icon: "terminal", action: () => store.openQueryTab(c.name) },
    { label: "测试连接", icon: "bolt", action: () => testConn(c) },
    { separator: true },
  ];
  if (kind === "redis") {
    items.push({ label: "新建数据库", icon: "database", action: () => promptRedisDb(c.name) });
  } else {
    items.push({ label: kind === "mongodb" ? "新建数据库" : "新增数据库", icon: "database", action: () => createDatabase(c.name) });
  }
  items.push(
    { label: "导入数据", icon: "upload", action: () => store.openImportTab(c.name, db, "") },
    { label: "导出数据", icon: "download", action: () => store.openExportTab(c.name, db, "") },
    { separator: true },
    { label: "编辑连接", icon: "edit", action: () => emit("edit-conn", c) },
    { label: "刷新数据库", icon: "refresh", action: () => refreshConn(c.name) },
    { label: "复制名称", icon: "copy", action: () => copyName(c.name) },
    { label: "关闭连接", icon: "power", danger: true, disabled: !store.openConns.includes(c.name), action: () => closeConn(c) },
    { label: "删除连接", icon: "trash", danger: true, action: () => removeConn(c) },
  );
  openMenu(e, items);
}

// 新建 Redis 逻辑库：输入下标（dbN / N）后加入树并加载其键。
async function promptRedisDb(conn: string) {
  const name = await store.promptText({
    title: "新建数据库",
    message: "输入 Redis 逻辑库下标，例如 db2 或 2（需在服务端 databases 配置范围内）。",
    label: "逻辑库",
    value: "db0",
    confirmText: "添加",
  });
  if (!name) return;
  const clean = name.trim().replace(/^db/i, "");
  if (!/^\d+$/.test(clean)) {
    store.setStatus("非法逻辑库下标", "error");
    return;
  }
  await store.createRedisDb(conn, `db${clean}`);
  store.setStatus(`已添加数据库 db${clean}`, "ok");
}

// 关闭已打开的连接，释放底层连接池等资源。
async function closeConn(c: ConnectionConfig) {
  try {
    await api.closeConnection(c.name);
    await store.loadOpenConns();
    store.setStatus(`已关闭连接 ${c.name}`, "ok");
  } catch (e: any) {
    store.setStatus(`关闭连接失败：${e?.message ?? e}`, "error");
  }
}

function menuDb(e: MouseEvent, conn: string, db: string) {
  selectedKey.value = `d:${conn}|${db}`;
  const kind = connKind(conn);
  const items: MenuItem[] = [
    { label: "新建查询", icon: "terminal", action: () => store.openQueryTab(conn) },
    { label: "刷新对象", icon: "refresh", action: () => refreshObjects(conn, db) },
    { separator: true },
    { label: "导入数据", icon: "upload", action: () => store.openImportTab(conn, db, "") },
    { label: "导出数据", icon: "download", action: () => store.openExportTab(conn, db, "") },
  ];
  if (kind === "redis") {
    items.push({ label: "新建 key", icon: "key", action: () => store.openRedisKeyTab(conn, db, "") });
  } else if (kind === "mongodb") {
    items.push({ label: "新建集合", icon: "layers", action: () => store.openMongoCollectionTab(conn, db, "") });
  } else {
    items.push(
      {
        label: "新建表",
        icon: "table",
        disabled: !store.hasCap(conn, "create_table"),
        action: () => store.openDesignerTab(conn, db),
      },
      { label: "导出整个数据库", icon: "download", disabled: !relational(connKind(conn)), action: () => store.openExportTab(conn, db, "") },
      { separator: true },
      { label: "修改数据库", icon: "settings", action: () => alterDatabase(conn, db) },
      { label: "重命名数据库", icon: "edit", action: () => renameDatabase(conn, db) },
      { label: "删除数据库", icon: "trash", danger: true, action: () => dropDatabase(conn, db) },
    );
  }
  items.push({ separator: true }, { label: "复制名称", icon: "copy", action: () => copyName(db) });
  openMenu(e, items);
}

function menuObj(e: MouseEvent, conn: string, db: string, o: ObjectInfo) {
  selectedKey.value = `o:${conn}|${db}|${o.name}`;
  const ref = objRef(db, o.name);
  // Redis 的「对象」即键：只提供键级操作，不展示表结构 / 复制表 / 截断等关系型功能。
  if (connKind(conn) === "redis") {
    openMenu(e, [
      { label: "打开", icon: "key", action: () => store.openRedisKeyTab(conn, db, o.name) },
      { separator: true },
      { label: "重命名", icon: "edit", action: () => renameObject(conn, db, o) },
      { label: "删除", icon: "trash", danger: true, action: () => dropObject(conn, db, o) },
      { separator: true },
      { label: "复制名称", icon: "copy", action: () => copyName(o.name) },
    ]);
    return;
  }
  if (connKind(conn) === "mongodb") {
    openMenu(e, [
      { label: "浏览数据", icon: "table", action: () => store.openDataTab(conn, ref) },
      { label: "查看 / 修改结构", icon: "column", action: () => store.openMongoCollectionTab(conn, db, o.name) },
      { separator: true },
      { label: "导入数据", icon: "upload", action: () => store.openImportTab(conn, db, o.name) },
      { label: "导出数据", icon: "download", action: () => store.openExportTab(conn, db, o.name) },
      { separator: true },
      { label: "重命名", icon: "edit", action: () => renameObject(conn, db, o) },
      { label: "删除", icon: "trash", danger: true, action: () => dropObject(conn, db, o) },
      { separator: true },
      { label: "复制名称", icon: "copy", action: () => copyName(o.name) },
    ]);
    return;
  }
  openMenu(e, [
    { label: "浏览数据", icon: "table", action: () => store.openDataTab(conn, ref) },
    { label: "查看 / 修改结构", icon: "column", action: () => store.openStructureTab(conn, ref) },
    { label: "复制表结构", icon: "copy", disabled: !relational(connKind(conn)), action: () => copyTable(conn, db, o, false) },
    { label: "复制表结构和数据", icon: "copy", disabled: !relational(connKind(conn)), action: () => copyTable(conn, db, o, true) },
    { separator: true },
    { label: "导入数据", icon: "upload", action: () => store.openImportTab(conn, db, o.name) },
    { label: "导出数据", icon: "download", action: () => store.openExportTab(conn, db, o.name) },
    { separator: true },
    { label: "截断数据 (TRUNCATE)", icon: "bolt", danger: true, disabled: !store.hasCap(conn, "truncate_table"), action: () => runTableAction(conn, db, o, "truncate") },
    { label: "清空数据 (DELETE)", icon: "trash", danger: true, disabled: !store.hasCap(conn, "empty_table"), action: () => runTableAction(conn, db, o, "empty") },
    { separator: true },
    { label: "刷新对象", icon: "refresh", action: () => refreshObjects(conn, db) },
    { label: "重命名", icon: "edit", action: () => renameObject(conn, db, o) },
    { label: "删除", icon: "trash", danger: true, action: () => dropObject(conn, db, o) },
    { separator: true },
    { label: "复制名称", icon: "copy", action: () => copyName(o.name) },
  ]);
}

// 单击对象：选中该节点（高亮在右键时保持），并激活所属连接。
function selectObj(conn: string, db: string, o: ObjectInfo) {
  selectedKey.value = `o:${conn}|${db}|${o.name}`;
  store.activate(conn);
}

// 双击对象：Redis 键打开键值编辑器；其它类型打开数据浏览。
function onObjDblClick(conn: string, db: string, o: ObjectInfo) {
  if (connKind(conn) === "redis") {
    store.openRedisKeyTab(conn, db, o.name);
    return;
  }
  store.openDataTab(conn, objRef(db, o.name));
}

const kindIcon: Record<string, string> = {
  mongodb: "layers",
  redis: "key",
  sqlite: "file",
};
const kindBadge: Record<string, string> = {
  mysql: "badge-info",
  postgresql: "badge-info",
  sqlserver: "badge-danger",
  oracle: "badge-warn",
  mongodb: "badge-ok",
  redis: "badge-danger",
  sqlite: "",
};
function objIcon(o: ObjectInfo) {
  if (o.type === "view") return "view";
  if (o.type === "collection") return "layers";
  if (o.type === "key") return "key";
  return "table";
}
</script>

<template>
  <div class="tree-wrap">
    <div class="tree-head">
      <span class="row" style="gap: 6px; font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: .06em">
        <Icon name="layers" :size="14" /> 资源管理器
      </span>
      <span class="grow"></span>
      <button class="btn btn-icon" title="新建连接" @click="$emit('edit-conn', null)">
        <Icon name="plus" :size="14" />
      </button>
      <button class="btn btn-icon" title="全部折叠" @click="collapseAll">
        <Icon name="chevrons-left" :size="14" />
      </button>
      <button class="btn btn-icon" title="刷新全部" @click="refreshAll">
        <Icon name="refresh" :size="14" />
      </button>
    </div>

    <div class="tree-search">
      <Icon name="search" :size="14" />
      <input v-model="store.treeQuery" class="grow" placeholder="搜索连接 / 库 / 对象…" />
      <button v-if="store.treeQuery" class="btn btn-icon" style="width: 20px; height: 20px" title="清除" @click="store.treeQuery = ''">
        <Icon name="close" :size="12" />
      </button>
    </div>

    <div class="tree-list scroll">
      <template v-for="c in visibleConns()" :key="c.name">
        <div
          class="tree-node"
          :class="{ selected: selectedKey === `c:${c.name}` || store.activeConn === c.name }"
          @click="toggleConn(c)"
          @contextmenu.prevent="menuConn($event, c)"
        >
          <span class="caret">{{ connOpen(c.name) ? "▾" : "▸" }}</span>
          <span
            class="status-dot"
            :class="store.openConns.includes(c.name) ? 'on' : store.connStatus[c.name] === 'err' ? 'err' : ''"
            :title="store.openConns.includes(c.name) ? '已连接（会话已建立）' : (store.connStatus[c.name] === 'err' ? '连接异常' : '未建立会话')"
          ></span>
          <Icon :name="kindIcon[c.kind] || 'database'" :size="14" />
          <span class="label" :title="c.name">{{ c.name }}</span>
          <span class="badge" :class="kindBadge[c.kind]">{{ c.kind }}</span>
          <span class="node-actions">
            <button class="btn-icon" style="width: 20px; height: 20px" title="新建数据库" @click.stop="createDatabase(c.name)">
              <Icon name="plus" :size="13" />
            </button>
            <button class="btn-icon" style="width: 20px; height: 20px" title="新建查询" @click.stop="store.openQueryTab(c.name)">
              <Icon name="terminal" :size="13" />
            </button>
            <button class="btn-icon" style="width: 20px; height: 20px" title="更多操作" @click.stop="menuConn($event, c)">
              <Icon name="more" :size="13" />
            </button>
          </span>
        </div>

        <div v-if="connOpen(c.name)" class="tree-children">
          <div v-if="loadingConns[c.name]" class="loading-line" style="padding: 4px 8px">
            <span class="spinner"></span> 正在读取数据库…
          </div>
          <template v-for="db in visibleDbs(c.name)" :key="db.name">
            <div
              class="tree-node"
              :class="{ selected: selectedKey === `d:${c.name}|${db.name}` }"
              @click="toggleDb(c.name, db.name)"
              @dblclick="onDbDblClick(c.name, db.name)"
              @contextmenu.prevent="menuDb($event, c.name, db.name)"
            >
              <span class="caret">{{ dbOpen(c.name, db.name) ? "▾" : "▸" }}</span>
              <Icon name="folder" :size="14" style="color: var(--warn)" />
              <span class="label" :title="db.name">{{ db.name }}</span>
              <span class="node-actions">
                <button class="btn-icon" style="width: 20px; height: 20px" :title="connKind(c.name) === 'redis' ? '新建 key' : connKind(c.name) === 'mongodb' ? '新建集合' : '新建表'" @click.stop="connKind(c.name) === 'redis' ? store.openRedisKeyTab(c.name, db.name, '') : connKind(c.name) === 'mongodb' ? store.openMongoCollectionTab(c.name, db.name, '') : store.openDesignerTab(c.name, db.name)">
                  <Icon name="plus" :size="13" />
                </button>
                <button class="btn-icon" style="width: 20px; height: 20px" title="更多操作" @click.stop="menuDb($event, c.name, db.name)">
                  <Icon name="more" :size="13" />
                </button>
              </span>
            </div>
            <div v-if="dbOpen(c.name, db.name)" class="tree-children">
              <div v-if="store.objectsLoading[store.cacheKey(c.name, db.name)]" class="loading-line" style="padding: 4px 8px">
                <span class="spinner"></span> 正在读取对象…
              </div>
              <div
                v-for="o in visibleObjs(c.name, db.name)"
                :key="o.name"
                class="tree-node obj"
                :class="{ selected: selectedKey === `o:${c.name}|${db.name}|${o.name}` }"
                @click="selectObj(c.name, db.name, o)"
                @dblclick="onObjDblClick(c.name, db.name, o)"
                @contextmenu.prevent="menuObj($event, c.name, db.name, o)"
              >
                <Icon :name="objIcon(o)" :size="13" style="color: var(--text-dim)" />
                <span class="label" :title="o.name">{{ o.name }}</span>
                <span class="badge">{{ o.type }}</span>
                <span class="node-actions">
                  <button v-if="connKind(c.name) === 'redis'" class="btn-icon" style="width: 20px; height: 20px" title="打开" @click.stop="store.openRedisKeyTab(c.name, db.name, o.name)">
                    <Icon name="key" :size="12" />
                  </button>
                  <button v-else-if="connKind(c.name) === 'mongodb'" class="btn-icon" style="width: 20px; height: 20px" title="浏览数据" @click.stop="store.openDataTab(c.name, objRef(db.name, o.name))">
                    <Icon name="table" :size="12" />
                  </button>
                  <button v-else class="btn-icon" style="width: 20px; height: 20px" title="浏览数据" @click.stop="store.openDataTab(c.name, objRef(db.name, o.name))">
                    <Icon name="table" :size="12" />
                  </button>
                  <button v-if="connKind(c.name) === 'mongodb'" class="btn-icon" style="width: 20px; height: 20px" title="查看 / 修改结构" @click.stop="store.openMongoCollectionTab(c.name, db.name, o.name)">
                    <Icon name="column" :size="12" />
                  </button>
                  <button v-else-if="connKind(c.name) !== 'redis'" class="btn-icon" style="width: 20px; height: 20px" title="查看结构" @click.stop="store.openStructureTab(c.name, objRef(db.name, o.name))">
                    <Icon name="column" :size="12" />
                  </button>
                </span>
              </div>
              <div v-if="!visibleObjs(c.name, db.name).length && !store.objectsLoading[store.cacheKey(c.name, db.name)]" class="faint" style="padding: 4px 8px; font-size: 11px">
                {{ connKind(c.name) === "redis" ? "双击数据库打开键搜索" : "暂无匹配对象" }}
              </div>
            </div>
          </template>
          <div v-if="!visibleDbs(c.name).length && !loadingConns[c.name]" class="faint" style="padding: 4px 8px; font-size: 11px">
            暂无数据库
          </div>
        </div>
      </template>

      <EmptyState
        v-if="!visibleConns().length"
        :compact="true"
        :icon="q ? 'search' : 'database'"
        :title="q ? '没有匹配的连接' : '还没有连接'"
        :description="q ? '试试其他关键字，或清除搜索。' : '新建一个连接即可开始浏览数据库。'"
      >
        <button v-if="q" class="btn btn-sm" @click="store.treeQuery = ''">清除搜索</button>
        <button v-else class="btn btn-sm btn-primary" @click="$emit('edit-conn', null)">新建连接</button>
      </EmptyState>
    </div>

    <div class="tree-foot faint">右键节点可查看更多操作 · 双击对象浏览数据</div>

    <ContextMenu :open="menu.open" :x="menu.x" :y="menu.y" :items="menu.items" @close="menu.open = false" />
  </div>
</template>

<style scoped>
.tree-wrap { display: flex; flex-direction: column; height: 100%; background: var(--surface); }
.tree-head { display: flex; align-items: center; gap: 2px; padding: 7px 8px; border-bottom: 1px solid var(--border); flex-shrink: 0; color: var(--text-dim); }
.tree-search { display: flex; align-items: center; gap: 6px; padding: 7px 8px; border-bottom: 1px solid var(--border); flex-shrink: 0; color: var(--text-dim); }
.tree-search input { background: transparent; border: 0; padding: 2px 0; box-shadow: none; }
.tree-search input:focus { box-shadow: none; border: 0; }
.tree-list { flex: 1; min-height: 0; padding: 5px; }
.tree-foot { padding: 5px 9px; border-top: 1px solid var(--border); font-size: 10px; flex-shrink: 0; }
.obj .label { font-size: 12px; }
</style>
