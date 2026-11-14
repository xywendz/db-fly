<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useWorkspace } from "@/stores/workspace";
import * as api from "@/api/client";
import type { SavedQuery } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";
import Modal from "./ui/Modal.vue";

const KINDS = ["mysql", "postgresql", "sqlserver", "oracle", "sqlite", "mongodb", "elasticsearch", "redis"];

const store = useWorkspace();
const items = ref<SavedQuery[]>([]);
const query = ref("");
const folderFilter = ref("");
const loading = ref(false);

const editing = ref(false);
const saving = ref(false);
const form = reactive<{ name: string; folder: string; kind: string; connection: string; text: string }>({
  name: "",
  folder: "",
  kind: "",
  connection: "",
  text: "",
});

const rootLabel = "（根目录）";

const folders = computed(() => {
  const set = new Set<string>();
  for (const it of items.value) if (it.folder) set.add(it.folder);
  return [...set].sort();
});

const grouped = computed(() => {
  const q = query.value.trim().toLowerCase();
  const fq = folderFilter.value;
  const match = (it: SavedQuery) =>
    (!q ||
      it.name.toLowerCase().includes(q) ||
      it.text.toLowerCase().includes(q) ||
      (it.connection || "").toLowerCase().includes(q) ||
      (it.kind || "").toLowerCase().includes(q)) &&
    (!fq || (it.folder || "") === fq);
  const root: SavedQuery[] = [];
  const map = new Map<string, SavedQuery[]>();
  for (const it of items.value) {
    if (!match(it)) continue;
    const f = it.folder || "";
    if (!f) root.push(it);
    else {
      if (!map.has(f)) map.set(f, []);
      map.get(f)!.push(it);
    }
  }
  const groups: { folder: string; items: SavedQuery[] }[] = [];
  if (root.length) groups.push({ folder: "", items: root });
  for (const f of [...map.keys()].sort()) groups.push({ folder: f, items: map.get(f)! });
  return groups;
});

onMounted(refresh);
watch(() => store.favoritesRev, refresh);

async function refresh() {
  loading.value = true;
  try {
    items.value = await api.listSavedQueries();
  } catch (e: any) {
    store.setStatus(e?.message ?? "收藏加载失败", "error");
  } finally {
    loading.value = false;
  }
}

function fmtTime(v?: string) {
  if (!v) return "";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString();
}

function open(it: SavedQuery) {
  store.openSQLTab(it.connection || store.activeConn || "", it.text, `收藏 · ${it.name}`);
}

async function copy(it: SavedQuery) {
  try {
    await navigator.clipboard.writeText(it.text);
    store.notify("语句已复制", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

function connKind(name: string): string {
  return store.connections.find((c) => c.name === name)?.kind || "";
}

function langOf(kind: string): string {
  if (kind === "redis") return "redis";
  if (kind === "mongodb") return "mongo_filter";
  if (kind === "elasticsearch") return "es_dsl";
  return "sql";
}

function openCreate() {
  form.name = "";
  form.folder = folderFilter.value;
  form.connection = store.activeConn;
  form.kind = connKind(store.activeConn);
  form.text = store.activeTab?.type === "query" ? store.activeTab.sql || "" : "";
  editing.value = true;
}

async function save() {
  const name = form.name.trim();
  if (!name) {
    store.setStatus("请填写收藏名称", "warn");
    return;
  }
  saving.value = true;
  try {
    const q: Omit<SavedQuery, "updated_at"> = {
      id: "",
      name,
      folder: form.folder.trim(),
      kind: form.kind || undefined,
      connection: form.connection || undefined,
      database: form.connection ? store.activeDb || undefined : undefined,
      language: form.kind ? langOf(form.kind) : "sql",
      text: form.text,
    };
    await api.saveSavedQuery(q);
    editing.value = false;
    await refresh();
    store.notify(`已收藏「${name}」`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "创建失败", "error");
  } finally {
    saving.value = false;
  }
}

async function del(it: SavedQuery) {
  const ok = await store.confirm({
    title: "删除收藏",
    message: `将删除收藏「${it.name}」${it.folder ? `（目录 ${it.folder}）` : ""}。`,
    danger: true,
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await api.deleteSavedQuery(it.id || it.name);
    await refresh();
    store.setStatus(`已删除收藏 ${it.name}`, "ok");
  } catch (e: any) {
    store.setStatus(e?.message ?? "删除失败", "error");
  }
}
</script>

<template>
  <div class="side">
    <div class="side-head">
      <Icon name="star" :size="15" />
      <strong>收藏</strong>
      <span class="badge">{{ items.length }}</span>
      <span class="grow"></span>
      <button class="btn btn-icon" title="新建收藏" @click="openCreate"><Icon name="plus" :size="14" /></button>
      <button class="btn btn-icon" title="刷新" @click="refresh"><Icon name="refresh" :size="14" /></button>
      <button class="btn btn-icon" title="关闭面板" @click="store.togglePanel('favorites')"><Icon name="close" :size="14" /></button>
    </div>

    <div class="side-search">
      <Icon name="search" :size="14" />
      <input v-model="query" class="grow" placeholder="搜索收藏…" />
      <select v-model="folderFilter" class="folder-filter" title="按目录筛选">
        <option value="">全部目录</option>
        <option v-for="f in folders" :key="f" :value="f">{{ f }}</option>
      </select>
    </div>

    <div class="side-list scroll">
      <div v-if="loading" class="loading-line" style="padding: 12px"><span class="spinner"></span> 加载中…</div>

      <template v-for="g in grouped" :key="g.folder || 'root'">
        <div class="group-head">
          <Icon name="folder" :size="12" />
          <span class="grow">{{ g.folder || rootLabel }}</span>
          <span class="faint">{{ g.items.length }}</span>
        </div>
        <div v-for="(it, i) in g.items" :key="it.id || g.folder + '/' + it.name + i" class="side-item">
          <div class="row" style="gap: 6px">
            <Icon name="star" :size="13" style="color: var(--warn)" />
            <strong class="grow truncate" :title="it.name" style="font-size: 12px">{{ it.name }}</strong>
            <span v-if="it.kind" class="badge badge-kind">{{ it.kind }}</span>
            <button class="btn btn-icon" style="width: 20px; height: 20px" title="打开查询" @click="open(it)"><Icon name="play" :size="12" /></button>
            <button class="btn btn-icon" style="width: 20px; height: 20px" title="复制语句" @click="copy(it)"><Icon name="copy" :size="12" /></button>
            <button class="btn btn-icon" style="width: 20px; height: 20px" title="删除" @click="del(it)"><Icon name="trash" :size="12" /></button>
          </div>
          <pre class="code side-pre" @click="open(it)">{{ it.text.slice(0, 180) || "（空语句）" }}</pre>
          <div class="row" style="gap: 6px">
            <span class="dim truncate grow" style="font-size: 10px">
              {{ it.connection || "未指定连接" }}{{ it.language && it.language !== "sql" ? " · " + it.language : "" }}
            </span>
            <span class="faint" style="font-size: 10px">{{ fmtTime(it.updated_at) }}</span>
          </div>
        </div>
      </template>

      <EmptyState
        v-if="!loading && !grouped.length"
        icon="star"
        title="暂无收藏"
        :description="query || folderFilter ? '没有匹配的收藏。' : '点击右上角「+」新建，或从历史记录加入。'"
        :compact="true"
      >
        <button v-if="!query && !folderFilter" class="btn btn-sm" @click="openCreate">新建收藏</button>
      </EmptyState>
    </div>

    <Modal :open="editing" title="新建收藏" :width="'540px'" :close-on-mask="false" @close="editing = false">
      <div class="fav-form">
        <div class="field"><label class="field-label">名称 *</label><input v-model="form.name" placeholder="例如：常用统计" /></div>
        <div class="field"><label class="field-label">目录</label>
          <input v-model="form.folder" list="fav-folders" placeholder="留空为根目录，如 运维 / 报表" />
          <datalist id="fav-folders"><option v-for="f in folders" :key="f" :value="f"></option></datalist>
        </div>
        <div class="field"><label class="field-label">数据库类型</label>
          <select v-model="form.kind">
            <option value="">自动（按连接）</option>
            <option v-for="k in KINDS" :key="k" :value="k">{{ k }}</option>
          </select>
        </div>
        <div class="field"><label class="field-label">关联连接</label>
          <select v-model="form.connection">
            <option value="">未指定</option>
            <option v-for="c in store.connections" :key="c.name" :value="c.name">{{ c.name }}（{{ c.kind }}）</option>
          </select>
        </div>
        <div class="field">
          <label class="field-label">语句 {{ form.text ? "" : "（可留空，稍后在查询页补充）" }}</label>
          <textarea v-model="form.text" rows="7" placeholder="SELECT ..."></textarea>
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="editing = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="save">
          <span v-if="saving" class="spinner" style="width: 12px; height: 12px"></span>
          <Icon v-else name="save" :size="13" /> 保存收藏
        </button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.side { display: flex; flex-direction: column; height: 100%; }
.side-head { display: flex; align-items: center; gap: 6px; padding: 8px 9px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
.side-search { display: flex; align-items: center; gap: 6px; padding: 7px 9px; border-bottom: 1px solid var(--border); color: var(--text-dim); flex-shrink: 0; }
.side-search input { background: transparent; border: 0; box-shadow: none; padding: 2px 0; }
.folder-filter { background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 2px 4px; font-size: 11px; }
.side-list { flex: 1; min-height: 0; padding: 6px; }
.group-head { display: flex; align-items: center; gap: 5px; padding: 6px 4px 3px; color: var(--text-faint); font-size: 11px; font-weight: 600; text-transform: uppercase; letter-spacing: .03em; }
.group-head .grow { flex: 1; }
.side-item { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 8px; margin-bottom: 6px; background: var(--surface-2); }
.side-pre { margin: 6px 0; max-height: 58px; overflow: hidden; color: var(--text-dim); font-size: 10px; line-height: 1.5; white-space: pre-wrap; word-break: break-word; cursor: pointer; }
.side-pre:hover { color: var(--text); }
.badge-kind { background: var(--accent-soft); color: var(--accent); border: 1px solid var(--accent-border); text-transform: lowercase; }
.fav-form { display: flex; flex-direction: column; gap: 10px; }
.fav-form .field { display: flex; flex-direction: column; gap: 4px; }
.fav-form .field-label { font-size: 11px; color: var(--text-faint); }
.fav-form input, .fav-form select, .fav-form textarea { width: 100%; box-sizing: border-box; }
.fav-form textarea { resize: vertical; font-family: var(--mono); font-size: 12px; line-height: 1.5; }
</style>
