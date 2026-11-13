<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useWorkspace } from "@/stores/workspace";
import * as api from "@/api/client";
import type { QueryHistoryEntry, SavedQuery } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const store = useWorkspace();
const items = ref<QueryHistoryEntry[]>([]);
const query = ref("");
const loading = ref(false);

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return items.value;
  return items.value.filter(
    (it) =>
      (it.text || "").toLowerCase().includes(q) ||
      (it.connection || "").toLowerCase().includes(q) ||
      (it.database || "").toLowerCase().includes(q) ||
      (it.action || "").toLowerCase().includes(q),
  );
});

onMounted(refresh);

async function refresh() {
  loading.value = true;
  try {
    items.value = await api.listHistory(300);
  } catch (e: any) {
    store.setStatus(e?.message ?? "历史加载失败", "error");
  } finally {
    loading.value = false;
  }
}

function fmtTime(v?: string) {
  if (!v) return "";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toLocaleString(undefined, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function open(it: QueryHistoryEntry) {
  if (!it.text) return;
  store.openSQLTab(it.connection || store.activeConn || "", it.text, `历史 · ${it.action || "query"}`);
}

async function copy(it: QueryHistoryEntry) {
  try {
    await navigator.clipboard.writeText(it.text || "");
    store.notify("语句已复制", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

async function favorite(it: QueryHistoryEntry) {
  if (!it.text) return;
  const name = await store.promptText({
    title: "加入收藏",
    message: "给这条语句起一个便于查找的名称。",
    label: "收藏名称",
    value: (it.text || "").slice(0, 40),
    confirmText: "保存",
  });
  if (!name) return;
  try {
    const kind = store.connections.find((c) => c.name === it.connection)?.kind || "";
    const q: Omit<SavedQuery, "updated_at"> = {
      id: "", name, folder: "", kind: kind || undefined,
      connection: it.connection, database: it.database,
      language: kind === "redis" ? "redis" : kind === "mongodb" ? "mongo_filter" : "sql",
      text: it.text,
    };
    await api.saveSavedQuery(q);
    store.favoritesRev++;
    store.notify(`已收藏「${name}」`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "收藏失败", "error");
  }
}

async function clearAll() {
  const ok = await store.confirm({
    title: "清空查询历史",
    message: "将删除本机保存的全部查询历史记录，此操作不可撤销。",
    danger: true,
    confirmText: "清空",
  });
  if (!ok) return;
  try {
    await api.clearHistory();
    await refresh();
    store.setStatus("查询历史已清空", "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "清空失败", "error");
  }
}
</script>

<template>
  <div class="side">
    <div class="side-head">
      <Icon name="history" :size="15" />
      <strong>查询历史</strong>
      <span class="badge">{{ items.length }}</span>
      <span class="grow"></span>
      <button class="btn btn-icon" title="刷新" @click="refresh"><Icon name="refresh" :size="14" /></button>
      <button class="btn btn-icon" title="清空历史" :disabled="!items.length" @click="clearAll"><Icon name="trash" :size="14" /></button>
      <button class="btn btn-icon" title="关闭面板" @click="store.togglePanel('history')"><Icon name="close" :size="14" /></button>
    </div>

    <div class="side-search">
      <Icon name="search" :size="14" />
      <input v-model="query" class="grow" placeholder="搜索历史语句…" />
    </div>

    <div class="side-list scroll">
      <div v-if="loading" class="loading-line" style="padding: 12px"><span class="spinner"></span> 加载中…</div>
      <div v-for="(it, i) in filtered" :key="it.id || i" class="side-item" @click="open(it)">
        <div class="row" style="gap: 6px">
          <span class="badge" :class="it.ok ? 'badge-ok' : 'badge-danger'">{{ it.ok ? "成功" : "失败" }}</span>
          <span class="badge">{{ it.action || "query" }}</span>
          <span class="grow"></span>
          <span class="faint" style="font-size: 10px">{{ fmtTime(it.at) }}</span>
        </div>
        <pre class="code side-pre">{{ (it.text || "(无语句)").slice(0, 220) }}</pre>
        <div class="row" style="gap: 6px">
          <span class="dim truncate grow" style="font-size: 10px">
            {{ it.connection || "—" }}{{ it.database ? " / " + it.database : "" }}
            {{ it.duration_ms ? " · " + it.duration_ms + "ms" : "" }}{{ it.rows ? " · " + it.rows + "行" : "" }}
          </span>
          <button class="btn btn-icon" style="width: 20px; height: 20px" title="复制语句" @click.stop="copy(it)"><Icon name="copy" :size="12" /></button>
          <button class="btn btn-icon" style="width: 20px; height: 20px" title="加入收藏" @click.stop="favorite(it)"><Icon name="star" :size="12" /></button>
        </div>
        <div v-if="it.error" class="field-error truncate" :title="it.error">{{ it.error }}</div>
      </div>

      <EmptyState
        v-if="!loading && !filtered.length"
        icon="history"
        title="暂无历史记录"
        :description="query ? '没有匹配的记录，试试其他关键字。' : '执行查询或导入后，记录会显示在这里。'"
        :compact="true"
      />
    </div>
  </div>
</template>

<style scoped>
.side { display: flex; flex-direction: column; height: 100%; }
.side-head { display: flex; align-items: center; gap: 6px; padding: 8px 9px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
.side-search { display: flex; align-items: center; gap: 6px; padding: 7px 9px; border-bottom: 1px solid var(--border); color: var(--text-dim); flex-shrink: 0; }
.side-search input { background: transparent; border: 0; box-shadow: none; padding: 2px 0; }
.side-list { flex: 1; min-height: 0; padding: 6px; }
.side-item { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 8px; margin-bottom: 6px; cursor: pointer; background: var(--surface-2); }
.side-item:hover { border-color: var(--accent); }
.side-pre { margin: 6px 0; max-height: 62px; overflow: hidden; color: var(--text-dim); font-size: 10px; line-height: 1.5; white-space: pre-wrap; word-break: break-word; }
</style>
