<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { QueryOutcome, SavedQuery } from "@/types/domain";
import ResultGrid from "./ResultGrid.vue";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const outcome = ref<QueryOutcome | null>(null);
const loading = ref(false);
const error = ref("");
const database = ref("");
const editorHeight = ref(180);
const showSave = ref(false);
const saving = ref(false);
const saveName = ref("");
const ta = ref<HTMLTextAreaElement | null>(null);
const gutter = ref<HTMLDivElement | null>(null);

const conn = computed(() => store.connections.find((c) => c.name === props.tab.conn));
const databases = computed(() => store.dbCache[props.tab.conn] || []);
const language = computed(() => {
  switch (conn.value?.kind) {
    case "mongodb": return "mongo_filter";
    case "redis": return "redis";
    default: return "sql";
  }
});
const sql = computed({
  get: () => props.tab.sql ?? "",
  set: (v: string) => { props.tab.sql = v; props.tab.dirty = true; },
});
const lineCount = computed(() => sql.value.split("\n").length);
const rowCount = computed(() => outcome.value?.pages.reduce((n, p) => n + p.rows.length, 0) ?? 0);

function syncScroll() {
  if (gutter.value && ta.value) gutter.value.scrollTop = ta.value.scrollTop;
}
function onTab(e: KeyboardEvent) {
  const el = e.target as HTMLTextAreaElement;
  const start = el.selectionStart;
  const end = el.selectionEnd;
  sql.value = sql.value.slice(0, start) + "  " + sql.value.slice(end);
  nextTick(() => { el.selectionStart = el.selectionEnd = start + 2; });
}

async function run(explain: boolean) {
  if (!sql.value.trim()) {
    error.value = "请输入要执行的语句。";
    return;
  }
  loading.value = true;
  error.value = "";
  outcome.value = null;
  try {
    const req = {
      conn: props.tab.conn,
      query: {
        language: language.value,
        text: sql.value,
        database: database.value || undefined,
      },
    };
    outcome.value = explain ? await api.explainQuery(req) : await api.executeQuery(req);
    props.tab.dirty = false;
    store.setStatus(explain ? "执行计划完成" : `查询完成 · ${rowCount.value} 行`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`查询失败：${error.value}`, "error");
  } finally {
    loading.value = false;
  }
}

function clearResult() {
  outcome.value = null;
  error.value = "";
}

async function saveFavorite() {
  if (!saveName.value.trim()) return;
  saving.value = true;
  try {
    const q: Omit<SavedQuery, "updated_at"> = {
      id: "",
      name: saveName.value.trim(),
      connection: props.tab.conn,
      database: database.value || undefined,
      language: language.value,
      text: sql.value,
    };
    await api.saveSavedQuery(q);
    store.notify(`已收藏「${q.name}」`, "ok");
    showSave.value = false;
    saveName.value = "";
  } catch (e: any) {
    store.setStatus(e?.message ?? "收藏失败", "error");
  } finally {
    saving.value = false;
  }
}

// 编辑器 / 结果区高度拖拽
let resizeState: { startY: number; startH: number } | null = null;
function startResize(e: MouseEvent) {
  resizeState = { startY: e.clientY, startH: editorHeight.value };
  window.addEventListener("mousemove", onResize);
  window.addEventListener("mouseup", stopResize);
}
function onResize(e: MouseEvent) {
  if (!resizeState) return;
  editorHeight.value = Math.max(90, Math.min(600, resizeState.startH + e.clientY - resizeState.startY));
}
function stopResize() {
  resizeState = null;
  window.removeEventListener("mousemove", onResize);
  window.removeEventListener("mouseup", stopResize);
}
onUnmounted(stopResize);
</script>

<template>
  <div class="qv">
    <div class="toolbar qv-bar">
      <button class="btn btn-primary" :disabled="loading" title="执行 (Ctrl+Enter)" @click="run(false)">
        <span v-if="loading" class="spinner" style="width: 12px; height: 12px"></span>
        <Icon v-else name="play" :size="13" /> 执行
      </button>
      <button class="btn" :disabled="loading" title="查看执行计划" @click="run(true)">
        <Icon name="bolt" :size="13" /> 解释
      </button>
      <span class="divider-v"></span>
      <button class="btn" title="保存当前语句为收藏" @click="showSave = !showSave">
        <Icon name="star" :size="13" /> 收藏
      </button>
      <button class="btn" :disabled="!outcome && !error" title="清空结果" @click="clearResult">
        <Icon name="close" :size="13" /> 清空
      </button>

      <span class="divider-v"></span>
      <span class="dim" style="font-size: 11px">数据库</span>
      <select v-model="database" style="min-width: 120px; max-width: 180px" :disabled="!databases.length">
        <option value="">（连接默认）</option>
        <option v-for="d in databases" :key="d.name" :value="d.name">{{ d.name }}</option>
      </select>

      <span class="grow"></span>
      <span class="badge badge-accent">{{ language }}</span>
      <span class="badge">{{ conn?.name || props.tab.conn }}</span>
      <span v-if="conn?.read_only" class="badge badge-warn"><Icon name="shield" :size="11" /> 只读</span>
      <span v-if="outcome" class="badge badge-ok">{{ rowCount }} 行 · {{ outcome.summary.duration_ms }} ms</span>
    </div>

    <div v-if="showSave" class="save-bar">
      <Icon name="star" :size="14" />
      <span class="dim nowrap">收藏名称</span>
      <input v-model="saveName" style="width: 220px" placeholder="例如：每日活跃用户统计" @keydown.enter="saveFavorite" />
      <button class="btn btn-sm btn-primary" :disabled="saving || !saveName.trim()" @click="saveFavorite">保存</button>
      <button class="btn btn-sm" @click="showSave = false">取消</button>
    </div>

    <div class="editor-pane" :style="{ height: editorHeight + 'px' }">
      <div ref="gutter" class="gutter code">
        <div v-for="n in lineCount" :key="n">{{ n }}</div>
      </div>
      <textarea
        ref="ta"
        v-model="sql"
        class="code sql-input"
        spellcheck="false"
        :placeholder="`在此输入 ${language} 语句…  Ctrl+Enter 执行`"
        @scroll="syncScroll"
        @keydown.ctrl.enter.prevent="run(false)"
        @keydown.tab.prevent="onTab"
      ></textarea>
    </div>

    <div class="h-splitter" title="拖动调整编辑器高度" @mousedown.prevent="startResize"></div>

    <div v-if="error" class="alert alert-error" style="margin: 8px 10px 0">
      <Icon name="alert" :size="15" />
      <span class="grow" style="white-space: pre-wrap; word-break: break-word">{{ error }}</span>
      <button class="btn btn-icon" style="width: 20px; height: 20px" title="关闭" @click="error = ''">
        <Icon name="close" :size="12" />
      </button>
    </div>

    <div class="result-pane">
      <ResultGrid :outcome="outcome" :loading="loading" />
    </div>
  </div>
</template>

<style scoped>
.qv { display: flex; flex-direction: column; height: 100%; min-height: 0; padding: 8px; gap: 0; }
.qv-bar { border: 1px solid var(--border); border-radius: var(--radius) var(--radius) 0 0; padding: 7px 9px; }
.save-bar { display: flex; align-items: center; gap: 8px; padding: 7px 10px; background: var(--surface-2); border: 1px solid var(--border); border-top: 0; flex-shrink: 0; }
.editor-pane { display: flex; min-height: 90px; border: 1px solid var(--border); border-top: 0; background: var(--bg); flex-shrink: 0; overflow: hidden; }
.gutter { width: 46px; flex-shrink: 0; overflow: hidden; padding: 10px 8px 10px 0; text-align: right; font-size: var(--fs-code); line-height: 20px; color: var(--text-faint); background: var(--surface); border-right: 1px solid var(--border); user-select: none; }
.sql-input { flex: 1; border: 0; border-radius: 0; resize: none; padding: 10px 12px; background: transparent; font-size: var(--fs-code); line-height: 20px; box-shadow: none; white-space: pre; overflow: auto; tab-size: 2; }
.sql-input:focus { border: 0; box-shadow: none; }
.h-splitter { height: 6px; cursor: row-resize; background: var(--border); flex-shrink: 0; border-radius: 2px; margin: 2px 0; }
.h-splitter:hover { background: var(--accent); }
.result-pane { flex: 1; min-height: 0; display: flex; }
</style>
