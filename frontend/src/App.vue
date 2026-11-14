<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import { useWorkspace, type Tab, type TabType } from "@/stores/workspace";
import * as api from "@/api/client";
import { runtimeMode } from "@/runtime";
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
const about = ref("");
const aboutInfo = ref<AppInfo | null>(null);
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
  try {
    const i = await api.info();
    about.value = i.version;
    aboutInfo.value = i;
  } catch {
    about.value = "无法获取运行时信息";
    aboutInfo.value = null;
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
      <aside v-if="!store.leftCollapsed" class="left" :style="{ width: store.leftWidth + 'px' }">
        <ConnectionTree @edit-conn="openConnDialog" />
      </aside>
      <div
        v-if="!store.leftCollapsed"
        class="splitter"
        :class="{ dragging: dragging === 'left' }"
        title="拖动调整宽度"
        @mousedown.prevent="startDrag('left', $event)"
      ></div>

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
          <KeepAlive :max="12">
            <ESConsoleView v-if="activeTab && activeTab.type === 'query' && activeIsES" :key="activeTab.id" :tab="activeTab" />
            <QueryView v-else-if="activeTab && activeTab.type === 'query'" :key="activeTab.id" :tab="activeTab" />
            <TableDataView v-else-if="activeTab && activeTab.type === 'data'" :key="activeTab.id" :tab="activeTab" />
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
    <Modal :open="!!about" title="关于 db-fly" width="440px" @close="about = ''">
      <div class="col" style="gap: 14px">
        <div class="row" style="gap: 14px">
          <img src="/logo.png" alt="db-fly" style="width: 56px; height: 56px; border-radius: 14px" />
          <div>
            <h3 style="font-size: 16px">db-fly</h3>
            <p class="dim" style="font-size: 12px">模块化、多数据库、跨平台的数据库客户端</p>
            <span class="badge badge-accent">{{ runtimeMode === "web" ? "Web + Vue 3" : "wails3 + Vue 3" }}</span>
          </div>
        </div>
        <div class="divider-h"></div>
        <template v-if="aboutInfo">
          <div class="row between"><span class="dim">版本</span><span class="mono">{{ aboutInfo.version }}</span></div>
          <div class="row between"><span class="dim">数据目录</span><span class="mono truncate" style="max-width: 280px" :title="aboutInfo.data_dir">{{ aboutInfo.data_dir }}</span></div>
          <div class="row between"><span class="dim">凭证实现</span><span>{{ aboutInfo.secrets_name }}</span></div>
          <div class="col" style="gap: 5px">
            <span class="dim">已注册数据库类型</span>
            <div class="row wrap" style="gap: 5px">
              <span v-for="k in aboutInfo.kinds" :key="k" class="badge">{{ k }}</span>
            </div>
          </div>
        </template>
        <p v-else class="dim">{{ about }}</p>
      </div>
      <template #footer>
        <span class="grow"></span>
        <button class="btn btn-primary" @click="about = ''">关闭</button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.app { display: flex; flex-direction: column; height: 100%; width: 100%; overflow: hidden; }
.body { display: flex; flex: 1; min-height: 0; width: 100%; overflow: hidden; }
.left { flex-shrink: 0; min-width: 0; overflow: hidden; }
/* 覆盖全局 .center 工具类的居中属性，保证页签栏贴左起排并铺满宽度。 */
.center { display: flex; flex-direction: column; align-items: stretch; justify-content: flex-start; flex: 1; width: 100%; min-width: 0; overflow: hidden; }
.center > .tabbar { width: 100%; justify-content: flex-start; }
.content { min-height: 0; display: flex; width: 100%; overflow: hidden; }
.content > * { flex: 1; min-width: 0; min-height: 0; max-width: 100%; }
.right { flex-shrink: 0; min-width: 0; overflow: hidden; border-left: 1px solid var(--border); background: var(--surface); }
</style>
