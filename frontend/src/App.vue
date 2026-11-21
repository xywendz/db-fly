<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import { useWorkspace, type Tab, type TabType } from "@/stores/workspace";
import * as api from "@/api/client";
import { checkForUpdates, runtimeMode } from "@/runtime";
import type { AppInfo, ConnectionConfig } from "@/types/domain";
import type { MenuItem } from "@/types/ui";
import Icon from "./components/ui/Icon.vue";
import Modal from "./components/ui/Modal.vue";
import ConfirmDialog from "./components/ui/ConfirmDialog.vue";
import ToastHost from "./components/ui/ToastHost.vue";
import ContextMenu from "./components/ui/ContextMenu.vue";
import Toolbar from "./components/Toolbar.vue";
import ConnectionTree from "./components/ConnectionTree.vue";
import StatusBar from "./components/StatusBar.vue";
import QueryView from "./components/QueryView.vue";
import TableDataView from "./components/TableDataView.vue";
import StructureView from "./components/StructureView.vue";
import ESStructureView from "./components/ESStructureView.vue";
import TableDesignerView from "./components/TableDesignerView.vue";
import ExportView from "./components/ExportView.vue";
import ImportView from "./components/ImportView.vue";
import RedisKeysView from "./components/RedisKeysView.vue";
import RedisKeyView from "./components/RedisKeyView.vue";
import MongoCollectionView from "./components/MongoCollectionView.vue";
import ESIndexView from "./components/ESIndexView.vue";
import ESConsoleView from "./components/ESConsoleView.vue";
import ConnectionDialog from "./components/ConnectionDialog.vue";
import HistoryPanel from "./components/HistoryPanel.vue";
import FavoritesPanel from "./components/FavoritesPanel.vue";
import SettingsDialog from "./components/SettingsDialog.vue";

const store = useWorkspace();

const connDialog = ref<{ open: boolean; editing: ConnectionConfig | null }>({ open: false, editing: null });
const settingsOpen = ref(false);
// 对话框开关与内容解耦：about 只承载加载失败时的提示文本，
// 点击「检查更新」不再清空内容导致弹窗关闭。
const aboutOpen = ref(false);
const about = ref("");
const aboutInfo = ref<AppInfo | null>(null);
const checkingUpdate = ref(false);
const updateStatus = ref("");
const tabMenu = ref({ open: false, x: 0, y: 0, items: [] as MenuItem[] });
const dragging = ref<"left" | "right" | null>(null);

const activeTab = computed(() => store.activeTab);
// Elasticsearch 没有数据库/表概念，查询使用专用控制台（method + 索引/action 路径 + DSL body），
// 而非通用 SQL 查询页。
const activeIsES = computed(
  () => !!activeTab.value && store.connections.find((c) => c.name === activeTab.value!.conn)?.kind === "elasticsearch"
);
const firstDb = computed(() => {
  const c = store.activeConn || store.connections[0]?.name;
  return store.activeDb || (store.dbCache[c || ""] || [])[0]?.name || "";
});

function openConnDialog(c: ConnectionConfig | null) {
  connDialog.value = { open: true, editing: c };
}
function target(p?: { conn?: string; database?: string }) {
  const conn = p?.conn || store.activeConn || store.connections[0]?.name || "";
  if (!conn) {
    openConnDialog(null);
    return null;
  }
  return { conn, database: p?.database || firstDb.value };
}
function openDesigner(p?: { conn: string; database: string }) {
  const t = target(p);
  if (t) store.openDesignerTab(t.conn, t.database);
}
function openImport(p?: { conn: string; database: string; table: string }) {
  const t = target(p);
  if (t) store.openImportTab(t.conn, t.database, p?.table || "");
}
function openExport(p?: { conn: string; database: string; table: string }) {
  const t = target(p);
  if (t) store.openExportTab(t.conn, t.database, p?.table || "");
}
function newQuery() {
  const conn = store.activeConn || store.connections[0]?.name;
  if (conn) store.openQueryTab(conn);
  else openConnDialog(null);
}

async function showAbout() {
  aboutOpen.value = true;
  updateStatus.value = "";
  about.value = "";
  aboutInfo.value = null;
  try {
    const i = await api.info();
    aboutInfo.value = i;
  } catch {
    about.value = "无法获取运行时信息";
  }
}

async function requestUpdateCheck() {
  checkingUpdate.value = true;
  updateStatus.value = "正在请求更新检查…";
  try {
    await checkForUpdates();
    updateStatus.value = runtimeMode === "gui"
      ? "已向更新器发起检查，请在弹出的更新窗口查看结果。"
      : "当前为 Web 模式，请在服务端执行更新。";
  } catch (e: any) {
    updateStatus.value = `检查更新失败：${e?.message ?? e}`;
  } finally {
    checkingUpdate.value = false;
  }
}

const TAB_ICONS: Record<TabType, string> = {
  query: "terminal",
  data: "table",
  structure: "column",
  designer: "edit",
  export: "download",
  import: "upload",
  "redis-keys": "key",
  "redis-key": "key",
  "mongo-coll": "layers",
  "es-index": "search",
};
function tabIcon(t: Tab) {
  return TAB_ICONS[t.type] ?? "file";
}

function menuTab(e: MouseEvent, t: Tab) {
  tabMenu.value = {
    open: true,
    x: e.clientX,
    y: e.clientY,
    items: [
      { label: "关闭", icon: "close", action: () => store.closeTab(t.id) },
      { label: "关闭其他", icon: "copy", disabled: store.tabs.length <= 1, action: () => store.closeOtherTabs(t.id) },
      { label: "关闭全部", icon: "trash", action: () => store.closeAllTabs() },
      { separator: true },
      { label: "复制标题", icon: "copy", action: () => navigator.clipboard?.writeText(t.title) },
    ],
  };
}

// ---- 分栏拖拽 ----
let dragState: { side: "left" | "right"; startX: number; startW: number } | null = null;
function startDrag(side: "left" | "right", e: MouseEvent) {
  dragState = { side, startX: e.clientX, startW: side === "left" ? store.leftWidth : store.rightWidth };
  dragging.value = side;
  document.body.classList.add("resizing");
  window.addEventListener("mousemove", onDrag);
  window.addEventListener("mouseup", stopDrag);
}
function onDrag(e: MouseEvent) {
  if (!dragState) return;
  const delta = e.clientX - dragState.startX;
  if (dragState.side === "left") {
    store.leftWidth = Math.max(200, Math.min(520, dragState.startW + delta));
  } else {
    store.rightWidth = Math.max(240, Math.min(600, dragState.startW - delta));
  }
}
function stopDrag() {
  dragState = null;
  dragging.value = null;
  document.body.classList.remove("resizing");
  window.removeEventListener("mousemove", onDrag);
  window.removeEventListener("mouseup", stopDrag);
}

// ---- 全局快捷键 ----
function onKey(e: KeyboardEvent) {
  if (!e.altKey || e.ctrlKey || e.metaKey) return;
  const key = e.key.toLowerCase();
  if (key === "n") { e.preventDefault(); newQuery(); }
  else if (key === "w") { e.preventDefault(); if (store.activeTabId) store.closeTab(store.activeTabId); }
  else if (key === "h") { e.preventDefault(); store.togglePanel("history"); }
  else if (key === "f") { e.preventDefault(); store.togglePanel("favorites"); }
}

onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => {
  window.removeEventListener("keydown", onKey);
  stopDrag();
});
</script>

<template>
  <div class="app">
    <Toolbar
      @new-connection="openConnDialog(null)"
      @new-query="newQuery"
      @new-table="openDesigner()"
      @import="openImport()"
      @export="openExport()"
      @settings="settingsOpen = true"
      @about="showAbout"
    />

    <div class="body">
      <template v-if="!store.leftCollapsed">
        <aside class="left" :style="{ width: store.leftWidth + 'px' }">
          <ConnectionTree @edit-conn="openConnDialog" />
        </aside>
        <div
          class="splitter"
          :class="{ dragging: dragging === 'left' }"
          title="拖动调整宽度"
          @mousedown.prevent="startDrag('left', $event)"
        ></div>
      </template>
      <aside v-else class="left-rail" title="展开资源管理器" @click="store.leftCollapsed = false">
        <button class="rail-toggle" title="展开资源管理器" @click="store.leftCollapsed = false">
          <Icon name="chevrons-right" :size="16" />
        </button>
        <span class="rail-label">资源管理器</span>
        <span v-if="store.openConns.length" class="rail-count" :title="`${store.openConns.length} 个连接已建立会话`">{{ store.openConns.length }}</span>
      </aside>

      <main class="center">
        <div class="tabbar">
          <div
            v-for="t in store.tabs"
            :key="t.id"
            class="tabitem"
            :class="{ active: t.id === store.activeTabId }"
            :title="`${t.conn} · ${t.title}`"
            @click="store.activeTabId = t.id"
            @click.middle="store.closeTab(t.id)"
            @contextmenu.prevent="menuTab($event, t)"
          >
            <Icon :name="tabIcon(t)" :size="13" />
            <span class="truncate" style="max-width: 170px">{{ t.title }}</span>
            <span v-if="t.dirty" class="status-dot" style="background: var(--warn)" title="有未执行的修改"></span>
            <button class="btn-icon" style="width: 18px; height: 18px; padding: 0" title="关闭" @click.stop="store.closeTab(t.id)">
              <Icon name="close" :size="12" />
            </button>
          </div>
          <button class="btn btn-icon" style="margin: 4px 6px" title="新建查询 (Alt+N)" @click="newQuery">
            <Icon name="plus" :size="14" />
          </button>
          <span class="grow"></span>
          <button
            v-if="store.tabs.length"
            class="btn btn-icon"
            style="margin: 4px 6px"
            title="关闭全部页签"
            @click="store.closeAllTabs()"
          >
            <Icon name="close" :size="14" />
          </button>
        </div>

        <div class="content grow">
          <KeepAlive>
            <ESConsoleView v-if="activeTab && activeTab.type === 'query' && activeIsES" :key="activeTab.id" :tab="activeTab" />
            <QueryView v-else-if="activeTab && activeTab.type === 'query'" :key="activeTab.id" :tab="activeTab" />
            <TableDataView v-else-if="activeTab && activeTab.type === 'data'" :key="activeTab.id" :tab="activeTab" />
            <ESStructureView v-else-if="activeTab && activeTab.type === 'structure' && activeIsES" :key="activeTab.id" :tab="activeTab" />
            <StructureView v-else-if="activeTab && activeTab.type === 'structure'" :key="activeTab.id" :tab="activeTab" />
            <TableDesignerView v-else-if="activeTab && activeTab.type === 'designer'" :key="activeTab.id" :tab="activeTab" />
            <ExportView v-else-if="activeTab && activeTab.type === 'export'" :key="activeTab.id" :tab="activeTab" />
            <ImportView v-else-if="activeTab && activeTab.type === 'import'" :key="activeTab.id" :tab="activeTab" />
            <RedisKeysView v-else-if="activeTab && activeTab.type === 'redis-keys'" :key="activeTab.id" :tab="activeTab" />
            <RedisKeyView v-else-if="activeTab && activeTab.type === 'redis-key'" :key="activeTab.id" :tab="activeTab" />
            <MongoCollectionView v-else-if="activeTab && activeTab.type === 'mongo-coll'" :key="activeTab.id" :tab="activeTab" />
            <ESIndexView v-else-if="activeTab && activeTab.type === 'es-index'" :key="activeTab.id" :tab="activeTab" />
            <div v-else :key="'welcome'" class="welcome">
              <img src="/logo.png" alt="db-fly" class="welcome-logo" />
              <h1>db-fly</h1>
              <p>模块化、多数据库、跨平台的数据库客户端。从左侧连接树浏览对象，或直接新建一个查询。</p>
              <div class="quick-grid">
                <button class="quick-card" @click="openConnDialog(null)">
                  <span class="qc-icon"><Icon name="plus" :size="17" /></span>
                  <span><strong>新建连接</strong><small>配置一个数据库</small></span>
                </button>
                <button class="quick-card" @click="newQuery">
                  <span class="qc-icon"><Icon name="terminal" :size="17" /></span>
                  <span><strong>新建查询</strong><small>编写并执行 SQL</small></span>
                </button>
                <button class="quick-card" @click="store.togglePanel('history')">
                  <span class="qc-icon"><Icon name="history" :size="17" /></span>
                  <span><strong>查询历史</strong><small>重放既往语句</small></span>
                </button>
                <button class="quick-card" @click="openImport()">
                  <span class="qc-icon"><Icon name="upload" :size="17" /></span>
                  <span><strong>导入数据</strong><small>CSV / XLSX / SQL</small></span>
                </button>
              </div>
              <div class="row dim" style="gap: 10px; margin-top: 12px; font-size: 11px">
                <span><span class="kbd">Alt+N</span> 新建查询</span>
                <span><span class="kbd">Alt+W</span> 关闭页签</span>
                <span><span class="kbd">Ctrl+Enter</span> 执行</span>
                <span><span class="kbd">右键</span> 更多操作</span>
              </div>
            </div>
          </KeepAlive>
        </div>
      </main>

      <template v-if="store.rightPanel !== 'none'">
        <div
          class="splitter"
          :class="{ dragging: dragging === 'right' }"
          title="拖动调整宽度"
          @mousedown.prevent="startDrag('right', $event)"
        ></div>
        <aside class="right" :style="{ width: store.rightWidth + 'px' }">
          <HistoryPanel v-if="store.rightPanel === 'history'" />
          <FavoritesPanel v-else />
        </aside>
      </template>
    </div>

    <StatusBar />

    <ToastHost />
    <ConfirmDialog />
    <ContextMenu :open="tabMenu.open" :x="tabMenu.x" :y="tabMenu.y" :items="tabMenu.items" @close="tabMenu.open = false" />

    <ConnectionDialog
      :open="connDialog.open"
      :editing="connDialog.editing"
      @close="connDialog.open = false"
      @saved="store.invalidateAll(); store.loadConnections()"
    />
    <SettingsDialog
      :open="settingsOpen"
      @close="settingsOpen = false"
      @saved="store.setStatus('设置已保存', 'ok')"
    />
    <Modal :open="aboutOpen" title="关于 db-fly" width="460px" @close="aboutOpen = false">
      <div class="about-dialog">
        <div class="about-hero">
          <img src="/logo.png" alt="db-fly" class="about-logo" />
          <div class="about-title">
            <h2>db-fly</h2>
            <p>模块化、多数据库、跨平台的数据库客户端</p>
            <span class="badge badge-accent">{{ runtimeMode === "web" ? "Web + Vue 3" : "wails3 + Vue 3" }}</span>
          </div>
        </div>

        <div class="about-section">
          <div class="about-section-title">运行信息</div>
          <template v-if="aboutInfo">
            <div class="about-info-row">
              <span class="about-label">当前版本</span>
              <span class="about-value mono">{{ aboutInfo.version }}</span>
            </div>
            <div class="about-info-row">
              <span class="about-label">数据目录</span>
              <span class="about-value mono about-path" :title="aboutInfo.data_dir">{{ aboutInfo.data_dir }}</span>
            </div>
            <div class="about-info-row">
              <span class="about-label">凭证实现</span>
              <span class="about-value">{{ aboutInfo.secrets_name }}</span>
            </div>
            <div class="about-info-row">
              <span class="about-label">已注册类型</span>
              <div class="about-kinds">
                <span v-for="k in aboutInfo.kinds" :key="k" class="badge">{{ k }}</span>
              </div>
            </div>
          </template>
          <p v-else class="dim" style="font-size: 12px; margin: 0">{{ about || "正在读取运行信息…" }}</p>
        </div>

        <div v-if="runtimeMode === 'gui'" class="about-section about-update">
          <div class="about-update-title">
            <Icon name="download" :size="15" /> 软件更新
          </div>
          <div class="about-update-row">
            <span class="about-value mono">{{ aboutInfo?.version || "—" }}</span>
            <button class="btn btn-primary" :disabled="checkingUpdate" @click="requestUpdateCheck">
              <Icon name="refresh" :size="13" /> {{ checkingUpdate ? "检查中…" : "检查更新" }}
            </button>
          </div>
          <p class="about-update-status" :class="{ 'about-update-error': updateStatus.startsWith('检查更新失败') }">
            {{ updateStatus || "从 GitHub Release 检查是否有新版本" }}
          </p>
        </div>
      </div>
      <template #footer>
        <span class="grow"></span>
        <button class="btn btn-primary" @click="aboutOpen = false">关闭</button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.app { display: flex; flex-direction: column; height: 100%; width: 100%; overflow: hidden; }
.body { display: flex; flex: 1; min-height: 0; width: 100%; overflow: hidden; }
.left { flex-shrink: 0; min-width: 0; overflow: hidden; }
.left-rail {
  flex-shrink: 0; width: 32px; display: flex; flex-direction: column; align-items: center; gap: 12px;
  padding: 8px 0; background: var(--surface); border-right: 1px solid var(--border); cursor: pointer;
}
.left-rail:hover { background: var(--surface-2); }
.rail-toggle {
  width: 24px; height: 24px; display: flex; align-items: center; justify-content: center;
  background: var(--surface-2); color: var(--text-dim); border: 1px solid var(--border);
  border-radius: var(--radius-sm); cursor: pointer;
}
.left-rail:hover .rail-toggle { background: var(--accent-soft); border-color: var(--accent); color: var(--accent-hover); }
.rail-label { writing-mode: vertical-rl; letter-spacing: .16em; font-size: 11px; color: var(--text-dim); user-select: none; }
.rail-count {
  min-width: 18px; height: 18px; padding: 0 4px; display: flex; align-items: center; justify-content: center;
  border-radius: 9px; background: var(--accent-soft); border: 1px solid var(--accent);
  color: var(--accent-hover); font-size: var(--fs-xxs); font-weight: 600;
}
/* 覆盖全局 .center 工具类的居中属性，保证页签栏贴左起排并铺满宽度。 */
.center { display: flex; flex-direction: column; align-items: stretch; justify-content: flex-start; flex: 1; width: 100%; min-width: 0; overflow: hidden; }
.center > .tabbar { width: 100%; justify-content: flex-start; }
.content { min-height: 0; display: flex; width: 100%; overflow: hidden; }
.content > * { flex: 1; min-width: 0; min-height: 0; max-width: 100%; }
.right { flex-shrink: 0; min-width: 0; overflow: hidden; border-left: 1px solid var(--border); background: var(--surface); }

/* ---- 关于 / 检查更新对话框 ---- */
.about-dialog { display: flex; flex-direction: column; gap: 14px; }
.about-hero { display: flex; align-items: center; gap: 14px; }
.about-logo { width: 56px; height: 56px; border-radius: 14px; box-shadow: var(--shadow); flex-shrink: 0; }
.about-title h2 { font-size: 17px; line-height: 1.2; }
.about-title p { font-size: var(--fs-xs); color: var(--text-dim); margin: 3px 0 7px; }
.about-section {
  border: 1px solid var(--border); border-radius: var(--radius-sm);
  background: var(--surface-2); padding: 10px 12px;
}
.about-section-title { font-size: var(--fs-xs); font-weight: 600; color: var(--text-dim); margin-bottom: 6px; }
.about-info-row { display: flex; align-items: flex-start; gap: 10px; padding: 5px 0; }
.about-info-row + .about-info-row { border-top: 1px dashed var(--border); }
.about-label { width: 72px; flex-shrink: 0; font-size: var(--fs-xs); color: var(--text-dim); }
.about-value { flex: 1; min-width: 0; font-size: var(--fs-xs); word-break: break-all; }
.about-path { max-height: 32px; overflow: auto; }
.about-kinds { display: flex; flex-wrap: wrap; gap: 5px; flex: 1; min-width: 0; }
.about-update-title {
  display: flex; align-items: center; gap: 6px;
  font-size: var(--fs-xs); font-weight: 600; color: var(--text-dim); margin-bottom: 10px;
}
.about-update-row { display: flex; align-items: center; gap: 10px; }
.about-update-status { margin: 9px 0 0; font-size: var(--fs-xxs); color: var(--text-faint); line-height: 1.5; }
.about-update-status.about-update-error { color: var(--danger); }
</style>
