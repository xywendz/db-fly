import { defineStore } from "pinia";
import { ref, computed, watch } from "vue";
import type {
  Capability,
  ConnectionConfig,
  DatabaseInfo,
  ObjectInfo,
  ObjectRef,
} from "@/types/domain";
import * as api from "@/api/client";
import { useSettings } from "./settings";

export type TabType = "query" | "data" | "structure" | "designer" | "export" | "import" | "redis-keys" | "redis-key" | "mongo-coll";
export type StatusLevel = "info" | "error" | "ok" | "warn";

export interface Tab {
  id: string;
  type: TabType;
  conn: string;
  title: string;
  ref?: ObjectRef;
  sql?: string;
  dirty?: boolean;
  /** 建表/导入/导出页使用的目标库与表。 */
  database?: string;
  table?: string;
  /** designer: create = 新表，alter = 修改已有表。 */
  mode?: "create" | "alter";
  format?: string;
  /** redis-key：已存在的键默认以只读方式打开，点击「修改」进入编辑态。 */
  readOnly?: boolean;
}

export interface Toast {
  id: number;
  text: string;
  level: StatusLevel;
}

export interface ConfirmState {
  open: boolean;
  mode: "confirm" | "prompt";
  title: string;
  message: string;
  confirmText: string;
  cancelText: string;
  danger: boolean;
  requireText?: string;
  subject?: string;
  inputLabel?: string;
  inputValue: string;
  inputPlaceholder?: string;
  resolve: (v: boolean | string | null) => void;
}

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmText?: string;
  cancelText?: string;
  danger?: boolean;
  requireText?: string;
  subject?: string;
}

export interface PromptOptions {
  title: string;
  message?: string;
  label?: string;
  value?: string;
  placeholder?: string;
  confirmText?: string;
}

interface Layout {
  leftWidth: number;
  rightWidth: number;
  leftCollapsed: boolean;
  rightPanel: "none" | "history" | "favorites";
}

const LAYOUT_KEY = "db-fly.layout.v1";
const defaultLayout: Layout = {
  leftWidth: 272,
  rightWidth: 320,
  leftCollapsed: false,
  rightPanel: "none",
};

function readLayout(): Layout {
  try {
    const raw = localStorage.getItem(LAYOUT_KEY);
    if (raw) return { ...defaultLayout, ...JSON.parse(raw) };
  } catch {
    /* localStorage 不可用时忽略 */
  }
  return { ...defaultLayout };
}

let tabSeq = 0;
let toastSeq = 0;

export const useWorkspace = defineStore("workspace", () => {
  const connections = ref<ConnectionConfig[]>([]);
  const capabilities = ref<Record<string, Capability[]>>({});
  const connStatus = ref<Record<string, "on" | "err" | "">>({});
  // openConns 记录已建立会话（已打开）的连接名，供资源管理器显示可关闭状态。
  const openConns = ref<string[]>([]);
  const activeConn = ref<string>("");
  const activeDb = ref<string>("");
  const treeQuery = ref("");

  const dbCache = ref<Record<string, DatabaseInfo[]>>({});
  const objectsCache = ref<Record<string, ObjectInfo[]>>({});
  const objectsLoading = ref<Record<string, boolean>>({});

  const tabs = ref<Tab[]>([]);
  const activeTabId = ref<string>("");

  const status = ref<{ text: string; level: StatusLevel }>({ text: "就绪", level: "info" });
  const toasts = ref<Toast[]>([]);
  // 收藏数据变化计数器，供历史面板收藏成功后通知收藏面板刷新。
  const favoritesRev = ref(0);

  const savedLayout = readLayout();
  const leftWidth = ref(savedLayout.leftWidth);
  const rightWidth = ref(savedLayout.rightWidth);
  const leftCollapsed = ref(savedLayout.leftCollapsed);
  const rightPanel = ref<Layout["rightPanel"]>(savedLayout.rightPanel);

  const activeTab = computed(() => tabs.value.find((t) => t.id === activeTabId.value));
  const capabilitiesOf = computed(() => capabilities.value[activeConn.value] ?? []);

  watch([leftWidth, rightWidth, leftCollapsed, rightPanel], () => {
    try {
      localStorage.setItem(
        LAYOUT_KEY,
        JSON.stringify({
          leftWidth: leftWidth.value,
          rightWidth: rightWidth.value,
          leftCollapsed: leftCollapsed.value,
          rightPanel: rightPanel.value,
        }),
      );
    } catch {
      /* 忽略 */
    }
  });

  function notify(text: string, level: StatusLevel = "info", ms = 4000) {
    const id = ++toastSeq;
    toasts.value.push({ id, text, level });
    if (ms > 0) window.setTimeout(() => dismissToast(id), ms);
  }

  function dismissToast(id: number) {
    const i = toasts.value.findIndex((t) => t.id === id);
    if (i >= 0) toasts.value.splice(i, 1);
  }

  function setStatus(text: string, level: StatusLevel = "info") {
    status.value = { text, level };
    if (level === "error" || level === "warn") notify(text, level, 6000);
    else if (level === "ok") notify(text, "ok", 2200);
  }

  /** 统一的确认对话框，替代原生 confirm。 */
  function confirm(opts: ConfirmOptions): Promise<boolean> {
    // 设置中可关闭危险操作的二次确认（默认开启，保持安全行为）。
    if (opts.danger && !useSettings().state.confirmDangerous) return Promise.resolve(true);
    return new Promise((resolve) => {
      confirmState.value = {
        open: true,
        mode: "confirm",
        title: opts.title,
        message: opts.message,
        confirmText: opts.confirmText ?? "确认",
        cancelText: opts.cancelText ?? "取消",
        danger: opts.danger ?? false,
        requireText: opts.requireText,
        subject: opts.subject,
        inputValue: "",
        resolve: (v) => resolve(v === true),
      };
    });
  }

  /** 统一的输入对话框，替代原生 prompt。取消返回 null。 */
  function promptText(opts: PromptOptions): Promise<string | null> {
    return new Promise((resolve) => {
      confirmState.value = {
        open: true,
        mode: "prompt",
        title: opts.title,
        message: opts.message ?? "",
        confirmText: opts.confirmText ?? "确定",
        cancelText: "取消",
        danger: false,
        inputLabel: opts.label,
        inputValue: opts.value ?? "",
        inputPlaceholder: opts.placeholder,
        resolve: (v) => resolve(typeof v === "string" ? v : null),
      };
    });
  }

  const confirmState = ref<ConfirmState>({
    open: false,
    mode: "confirm",
    title: "",
    message: "",
    confirmText: "确认",
    cancelText: "取消",
    danger: false,
    inputValue: "",
    resolve: () => undefined,
  });

  function resolveConfirm(value: boolean | string | null) {
    confirmState.value.resolve(value);
    confirmState.value.open = false;
  }

  async function loadConnections() {
    connections.value = await api.listConnections();
    if (!connections.value.find((c) => c.name === activeConn.value)) {
      activeConn.value = connections.value[0]?.name ?? "";
    }
    for (const c of connections.value) {
      if (!capabilities.value[c.name]) {
        try {
          capabilities.value[c.name] = await api.connectionCapabilities(c.name);
        } catch {
          capabilities.value[c.name] = [];
        }
      }
    }
  }

  async function loadOpenConns() {
    try {
      openConns.value = await api.openConnections();
    } catch {
      openConns.value = [];
    }
  }

  function hasCap(conn: string, cap: Capability): boolean {
    return (capabilities.value[conn] ?? []).includes(cap);
  }

  function invalidateAll() {
    dbCache.value = {};
    objectsCache.value = {};
    capabilities.value = {};
    connStatus.value = {};
  }

  async function loadDatabases(conn: string) {
    if (dbCache.value[conn]) return dbCache.value[conn];
    const dbs = await api.listDatabases(conn);
    dbCache.value[conn] = dbs;
    connStatus.value[conn] = "on";
    return dbs;
  }

  async function loadObjects(conn: string, database: string, schema = "") {
    const key = cacheKey(conn, database, schema);
    if (objectsCache.value[key]) return objectsCache.value[key];
    objectsLoading.value[key] = true;
    try {
      const objs = await api.listObjects(conn, database, schema);
      objectsCache.value[key] = objs;
      return objs;
    } catch (e) {
      connStatus.value[conn] = "err";
      throw e;
    } finally {
      objectsLoading.value[key] = false;
    }
  }

  function cacheKey(conn: string, database: string, schema = "") {
    return `${conn}|${database}|${schema}`;
  }

  function activate(conn: string) {
    activeConn.value = conn;
  }

  function openQueryTab(conn: string) {
    const id = `q${++tabSeq}`;
    tabs.value.push({ id, type: "query", conn, title: `查询 · ${conn}` });
    activeTabId.value = id;
    activeConn.value = conn;
  }

  function openSQLTab(conn: string, sql: string, title?: string) {
    const id = `q${++tabSeq}`;
    tabs.value.push({ id, type: "query", conn, title: title ?? `查询 · ${conn}`, sql });
    activeTabId.value = id;
    activeConn.value = conn;
  }

  /** 同一对象的同名页签复用，避免重复打开。 */
  function findTab(type: TabType, conn: string, ref: ObjectRef) {
    return tabs.value.find(
      (t) =>
        t.type === type &&
        t.conn === conn &&
        t.ref?.database === ref.database &&
        t.ref?.name === ref.name &&
        (t.ref?.schema ?? "") === (ref.schema ?? ""),
    );
  }

  function openDataTab(conn: string, ref: ObjectRef) {
    const existing = findTab("data", conn, ref);
    if (existing) {
      activeTabId.value = existing.id;
      activeConn.value = conn;
      leftCollapsed.value = false;
      return;
    }
    const id = `d${++tabSeq}`;
    tabs.value.push({ id, type: "data", conn, ref, title: `数据 · ${ref.name}` });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = ref.database;
    // 浏览表数据时始终保留左侧资源管理器，方便切换对象。
    leftCollapsed.value = false;
  }

  /** Redis 键搜索页：默认不加载全部 key，由用户在页内按模式检索（SCAN MATCH）。 */
  function openRedisKeysTab(conn: string, database: string) {
    const existing = tabs.value.find(
      (t) => t.type === "redis-keys" && t.conn === conn && t.database === database,
    );
    if (existing) {
      activeTabId.value = existing.id;
      activeConn.value = conn;
      leftCollapsed.value = false;
      return;
    }
    const id = `k${++tabSeq}`;
    tabs.value.push({
      id,
      type: "redis-keys",
      conn,
      database,
      ref: { database, name: "" },
      title: `键 · ${database}`,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  /** Redis 键编辑器页签：双击 key 或「新建键」打开；key 为空表示新建。 */
  // readOnly 仅对「已存在的键」生效：默认以只读（浏览）方式打开，点击「修改」进入编辑态。
  // 新建键（key 为空）始终为可编辑的创建态。
  function openRedisKeyTab(conn: string, database: string, key: string, readOnly = true) {
    if (key) {
      const existing = tabs.value.find(
        (t) =>
          t.type === "redis-key" &&
          t.conn === conn &&
          t.database === database &&
          (t.ref?.name ?? "") === key,
      );
      if (existing) {
        activeTabId.value = existing.id;
        activeConn.value = conn;
        leftCollapsed.value = false;
        return;
      }
    }
    const id = `rk${++tabSeq}`;
    const isCreate = !key;
    tabs.value.push({
      id,
      type: "redis-key",
      conn,
      database,
      ref: { database, name: key },
      title: key ? `键 · ${key}` : `新建键 · ${database}`,
      readOnly: isCreate ? false : readOnly,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  /** Redis「新建数据库」：把指定逻辑库下标加入树并显示，便于浏览尚未出现在 keyspace 的空库。 */
  async function createRedisDb(conn: string, dbName: string) {
    const list = dbCache.value[conn] ?? [];
    if (!list.some((d) => d.name === dbName)) {
      dbCache.value[conn] = [...list, { name: dbName } as DatabaseInfo];
    }
    try {
      await loadObjects(conn, dbName);
    } catch {
      /* 空库或越界下标，忽略 */
    }
  }

  /** Mongo 集合编辑器页签：name 为空表示新建集合；非空表示查看/修改集合结构（校验器/索引）。 */
  function openMongoCollectionTab(conn: string, database: string, name: string) {
    if (name) {
      const existing = tabs.value.find(
        (t) =>
          t.type === "mongo-coll" &&
          t.conn === conn &&
          t.database === database &&
          (t.ref?.name ?? "") === name,
      );
      if (existing) {
        activeTabId.value = existing.id;
        activeConn.value = conn;
        leftCollapsed.value = false;
        return;
      }
    }
    const id = `mc${++tabSeq}`;
    tabs.value.push({
      id,
      type: "mongo-coll",
      conn,
      database,
      ref: { database, name },
      title: name ? `集合 · ${name}` : `新建集合 · ${database}`,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  function openStructureTab(conn: string, ref: ObjectRef) {
    const existing = findTab("structure", conn, ref);
    if (existing) {
      activeTabId.value = existing.id;
      activeConn.value = conn;
      return;
    }
    const id = `s${++tabSeq}`;
    tabs.value.push({ id, type: "structure", conn, ref, title: `结构 · ${ref.name}` });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = ref.database;
  }

  /** 新建表 / 修改表结构：以页签形式打开设计器，不再使用弹窗。 */
  function openDesignerTab(conn: string, database: string, ref?: ObjectRef) {
    const mode: Tab["mode"] = ref ? "alter" : "create";
    if (ref) {
      const existing = tabs.value.find(
        (t) =>
          t.type === "designer" &&
          t.conn === conn &&
          t.mode === "alter" &&
          t.ref?.database === ref.database &&
          t.ref?.name === ref.name,
      );
      if (existing) {
        activeTabId.value = existing.id;
        leftCollapsed.value = false;
        return;
      }
    }
    const id = `n${++tabSeq}`;
    tabs.value.push({
      id,
      type: "designer",
      conn,
      database,
      table: ref?.name ?? "",
      ref,
      mode,
      title: ref ? `设计 · ${ref.name}` : `新建表 · ${database || conn}`,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  /** 导出向导页签：同一连接 + 库 + 表只保留一个。 */
  function openExportTab(conn: string, database: string, table = "") {
    const existing = tabs.value.find(
      (t) => t.type === "export" && t.conn === conn && t.database === database && t.table === table,
    );
    if (existing) {
      activeTabId.value = existing.id;
      leftCollapsed.value = false;
      return;
    }
    const id = `x${++tabSeq}`;
    tabs.value.push({
      id,
      type: "export",
      conn,
      database,
      table,
      title: table ? `导出 · ${table}` : `导出 · ${database || conn}`,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  /** 导入引导页签。 */
  function openImportTab(conn: string, database: string, table = "", format = "csv") {
    const existing = tabs.value.find(
      (t) => t.type === "import" && t.conn === conn && t.database === database && t.table === table,
    );
    if (existing) {
      activeTabId.value = existing.id;
      leftCollapsed.value = false;
      return;
    }
    const id = `i${++tabSeq}`;
    tabs.value.push({
      id,
      type: "import",
      conn,
      database,
      table,
      format,
      title: table ? `导入 · ${table}` : `导入 · ${database || conn}`,
    });
    activeTabId.value = id;
    activeConn.value = conn;
    activeDb.value = database;
    leftCollapsed.value = false;
  }

  function renameTab(id: string, title: string) {
    const t = tabs.value.find((x) => x.id === id);
    if (t) t.title = title;
  }

  function renameObjectTabs(conn: string, ref: ObjectRef, newName: string) {
    for (const t of tabs.value) {
      if (t.conn === conn && t.ref?.database === ref.database && t.ref.name === ref.name) {
        t.ref = { ...t.ref, name: newName };
        t.title = t.title.replace(ref.name, newName);
      }
    }
  }

  /** 关闭页签前若仍有未应用的修改，弹窗确认以避免误操作丢失更改。 */
  async function closeTab(id: string) {
    const t = tabs.value.find((x) => x.id === id);
    if (!t) return;
    if (t.dirty) {
      const ok = await confirm({
        title: "关闭页签",
        message: `「${t.title}」存在未应用的修改，关闭后将丢弃这些更改。`,
        confirmText: "关闭并丢弃",
        danger: true,
      });
      if (!ok) return;
    }
    const idx = tabs.value.findIndex((x) => x.id === id);
    if (idx >= 0) {
      tabs.value.splice(idx, 1);
      if (activeTabId.value === id) {
        activeTabId.value = tabs.value[Math.max(0, idx - 1)]?.id ?? "";
      }
    }
  }

  async function closeOtherTabs(id: string) {
    const others = tabs.value.filter((t) => t.id !== id);
    if (others.some((t) => t.dirty)) {
      const ok = await confirm({
        title: "关闭其他页签",
        message: "其中存在未应用的修改，关闭后将丢弃这些更改。",
        confirmText: "关闭并丢弃",
        danger: true,
      });
      if (!ok) return;
    }
    tabs.value = tabs.value.filter((t) => t.id === id);
    activeTabId.value = id;
  }

  async function closeAllTabs() {
    if (tabs.value.some((t) => t.dirty)) {
      const ok = await confirm({
        title: "关闭全部页签",
        message: "其中存在未应用的修改，关闭后将丢弃这些更改。",
        confirmText: "关闭并丢弃",
        danger: true,
      });
      if (!ok) return;
    }
    tabs.value = [];
    activeTabId.value = "";
  }

  function invalidateObjects(conn: string, database: string, schema = "") {
    delete objectsCache.value[cacheKey(conn, database, schema)];
  }

  function togglePanel(which: "history" | "favorites") {
    rightPanel.value = rightPanel.value === which ? "none" : which;
  }

  return {
    connections,
    capabilities,
    capabilitiesOf,
    connStatus,
    openConns,
    loadOpenConns,
    activeConn,
    activeDb,
    treeQuery,
    dbCache,
    objectsCache,
    objectsLoading,
    tabs,
    activeTabId,
    activeTab,
    status,
    toasts,
    favoritesRev,
    confirmState,
    leftWidth,
    rightWidth,
    leftCollapsed,
    rightPanel,
    setStatus,
    notify,
    dismissToast,
    confirm,
    promptText,
    resolveConfirm,
    loadConnections,
    hasCap,
    invalidateAll,
    loadDatabases,
    loadObjects,
    cacheKey,
    activate,
    openQueryTab,
    openSQLTab,
    openDataTab,
    openStructureTab,
    openRedisKeysTab,
    openRedisKeyTab,
    createRedisDb,
    openMongoCollectionTab,
    openDesignerTab,
    openExportTab,
    openImportTab,
    renameTab,
    renameObjectTabs,
    closeTab,
    closeOtherTabs,
    closeAllTabs,
    invalidateObjects,
    togglePanel,
  };
});
