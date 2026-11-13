<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import { useSettings } from "@/stores/settings";
import * as api from "@/api/client";
import type {
  FilterCondition,
  ObjectRef,
  QueryOutcome,
  Row,
  RowEdit,
  SortTerm,
  TableSchema,
} from "@/types/domain";
import ResultGrid from "./ResultGrid.vue";
import Icon from "./ui/Icon.vue";
import ContextMenu from "./ui/ContextMenu.vue";
import type { MenuItem } from "@/types/ui";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const refObj = computed<ObjectRef>(() => props.tab.ref!);
const outcome = ref<QueryOutcome | null>(null);
const loading = ref(false);
const error = ref("");
const settings = useSettings();
const maxRows = ref(settings.state.pageSize);
const page = ref(1);
const showFilters = ref(false);
const conditions = ref<FilterCondition[]>([]);
const sorts = ref<SortTerm[]>([]);
const schema = ref<TableSchema | null>(null);
const keyCols = ref<string[]>([]);
const originalRows = ref<Row[]>([]);

const updateBuffer = ref<Record<number, Record<string, string | null>>>({});
const deleteRows = ref<number[]>([]);
const selectedRows = ref<number[]>([]);
// 复制行：本页内存剪贴板，用于「插入复制的行」在表中新增重复记录。
const copiedRows = ref<Record<string, string | null>[]>([]);
const ctxMenu = ref<{ open: boolean; x: number; y: number; items: MenuItem[] }>({ open: false, x: 0, y: 0, items: [] });
const previewSQL = ref("");
const previewed = ref(false);

// 待新增行：直接作为表格末尾的空行就地填写，提交时转为 INSERT。
const drafts = ref<Record<string, string>[]>([]);
const applying = ref(false);

const pageCols = computed(() => outcome.value?.pages?.[0]?.columns ?? []);
const colNames = computed(() => pageCols.value.map((c) => c.name));
const activeKeys = computed(() => (schema.value?.columns ?? []).filter((c) => c.primary_key).map((c) => c.name));
const effectiveKeys = computed(() => (keyCols.value.length ? keyCols.value : activeKeys.value));
const loadedRows = computed(() => outcome.value?.pages?.[0]?.rows?.length ?? 0);
const canNext = computed(() => loadedRows.value >= maxRows.value);

// 草稿行转 INSERT 值：留空列不提供，输入 NULL 表示显式空值。
function draftToValues(d: Record<string, string>): Record<string, string | null> {
  const values: Record<string, string | null> = {};
  for (const [k, v] of Object.entries(d)) {
    const t = (v ?? "").trim();
    if (!t) continue;
    values[k] = t.toUpperCase() === "NULL" ? null : v;
  }
  return values;
}
function draftInserts(): Record<string, string | null>[] {
  return drafts.value.map(draftToValues).filter((v) => Object.keys(v).length > 0);
}
const pendingCount = computed(
  () => Object.keys(updateBuffer.value).length + deleteRows.value.length + draftInserts().length,
);
// 把待提交修改标记到页签上，关闭页签时据此弹窗提示。
watch(pendingCount, (n) => { if (props.tab) props.tab.dirty = n > 0; }, { immediate: true });

watch(maxRows, () => { page.value = 1; refresh(); });
watch(() => settings.state.pageSize, (v) => { maxRows.value = v; });

function clearPending() {
  updateBuffer.value = {};
  deleteRows.value = [];
  drafts.value = [];
  selectedRows.value = [];
  previewSQL.value = "";
  previewed.value = false;
}

async function refresh() {
  loading.value = true;
  error.value = "";
  clearPending();
  try {
    const req = {
      conn: props.tab.conn,
      query: {
        language: "sql",
        ref: refObj.value,
        conditions: conditions.value.filter((c) => c.column && (c.value ?? "") !== ""),
        order_terms: sorts.value.filter((s) => s.column),
        max_rows: maxRows.value,
        offset: (page.value - 1) * maxRows.value,
      },
    };
    const res = await api.executeQuery(req);
    // 空表：后端可能不返回结果集或列信息，用表结构补出列头，保证页面可展示并可直接新增行。
    if (!res.pages.length) res.pages.push({ columns: [], rows: [] });
    const first = res.pages[0];
    if (first && !first.columns.length && schema.value?.columns?.length) {
      first.columns = schema.value.columns.map((c) => ({ name: c.name, type: c.data_type || c.raw_type || "" }));
    }
    outcome.value = res;
    originalRows.value = res.pages[0]?.rows ?? [];
    store.setStatus(`${refObj.value.name} 已加载 ${loadedRows.value} 行`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`加载失败：${error.value}`, "error");
  } finally {
    loading.value = false;
  }
}

async function loadMeta() {
  try {
    schema.value = await api.describeObject(props.tab.conn, refObj.value);
  } catch { /* 结构信息非必需 */ }
  try {
    keyCols.value = await api.tableKeyColumns(props.tab.conn, refObj.value);
  } catch { keyCols.value = []; }
}

onMounted(async () => {
  await loadMeta();
  await refresh();
});

function goPage(delta: number) {
  const next = page.value + delta;
  if (next < 1 || (delta > 0 && !canNext.value)) return;
  page.value = next;
  refresh();
}
function onPageSizeChange(e: Event) {
  maxRows.value = Math.max(10, Math.min(5000, Number((e.target as HTMLInputElement).value) || settings.state.pageSize));
  settings.update({ pageSize: maxRows.value });
}

function addCond() {
  conditions.value.push({
    column: colNames.value[0] ?? "",
    operator: "=",
    value: "",
    join: conditions.value.length ? "AND" : "",
  });
}
function addSort() {
  sorts.value.push({ column: colNames.value[0] ?? "", desc: false });
}
function clearFilters() {
  conditions.value = [];
  sorts.value = [];
  page.value = 1;
  refresh();
}

function onCellEdit(p: { rowIndex: number; column: string; value: string; isNull: boolean }) {
  if (!updateBuffer.value[p.rowIndex]) updateBuffer.value[p.rowIndex] = {};
  updateBuffer.value[p.rowIndex][p.column] = p.isNull ? null : p.value;
  previewed.value = false;
}

function addDraftRow() {
  const row: Record<string, string> = {};
  for (const c of colNames.value) row[c] = "";
  drafts.value.push(row);
  previewed.value = false;
  nextTick(() => {
    const inputs = document.querySelectorAll<HTMLInputElement>(".draft-input");
    const n = colNames.value.length || 1;
    inputs[inputs.length - n]?.focus();
  });
}
function onDraftEdit(p: { rowIndex: number; column: string; value: string }) {
  const d = drafts.value[p.rowIndex];
  if (!d) return;
  d[p.column] = p.value;
  previewed.value = false;
}
function removeDraft(i: number) {
  drafts.value.splice(i, 1);
  previewed.value = false;
}
async function copyPreview() {
  try {
    await navigator.clipboard.writeText(previewSQL.value);
    store.notify("变更语句已复制", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

function markDeleted() {
  if (!selectedRows.value.length) return;
  deleteRows.value = [...new Set([...deleteRows.value, ...selectedRows.value])];
  selectedRows.value = [];
  previewed.value = false;
  store.notify(`已标记 ${deleteRows.value.length} 行待删除`, "warn");
}
function unmarkDeleted() {
  deleteRows.value = [];
  previewed.value = false;
}

// 复制选中的行到内存剪贴板（按列名记录各单元格文本 / NULL）。
function copySelectedRows() {
  if (!selectedRows.value.length) return;
  const cols = colNames.value;
  const rows: Record<string, string | null>[] = [];
  for (const ri of selectedRows.value) {
    const row = originalRows.value[ri] ?? [];
    const rec: Record<string, string | null> = {};
    for (let i = 0; i < cols.length; i++) {
      const c = row[i];
      rec[cols[i]] = c?.null ? null : (c?.text ?? null);
    }
    rows.push(rec);
  }
  copiedRows.value = rows;
  previewed.value = false;
  store.notify(`已复制 ${rows.length} 行，可右键「插入复制的行」`, "info");
}

// 插入复制的行：先按正常业务在表格下方新增一个空行，再把复制的字段值填入该空行，
// 完整保留源行各列（含主键），确认后可预览应用。
function insertCopiedRows() {
  if (!copiedRows.value.length) return;
  for (const r of copiedRows.value) {
    addDraftRow();
    const draft = drafts.value[drafts.value.length - 1];
    for (const [k, v] of Object.entries(r)) {
      draft[k] = v === null ? "NULL" : v;
    }
  }
  previewed.value = false;
  store.notify(`已在表格下方新增 ${copiedRows.value.length} 行并填入复制值，确认后预览应用`, "info");
}

function openGridMenu(e: MouseEvent) {
  ctxMenu.value = {
    open: true,
    x: e.clientX,
    y: e.clientY,
    items: [
      { label: `复制选中行${selectedRows.value.length ? ` (${selectedRows.value.length})` : ""}`, icon: "copy", disabled: !selectedRows.value.length, action: copySelectedRows },
      { label: `插入复制的行${copiedRows.value.length ? ` (${copiedRows.value.length})` : ""}`, icon: "paste", disabled: !copiedRows.value.length, action: insertCopiedRows },
    ],
  };
}

function buildEditRequest() {
  const inserts = draftInserts();
  if (!originalRows.value.length && !inserts.length) {
    store.setStatus("请先加载数据", "warn");
    return null;
  }
  const pairs = effectiveKeys.value
    .map((k) => ({ k, i: colNames.value.indexOf(k) }))
    .filter((x) => x.i >= 0);
  if (!pairs.length && (deleteRows.value.length || Object.keys(updateBuffer.value).length)) {
    store.setStatus("无法确定主键或唯一键，无法安全回写", "error");
    return null;
  }
  const keyOf = (ri: number): Record<string, string | null> => {
    const row = originalRows.value[ri] ?? [];
    const key: Record<string, string | null> = {};
    for (const { k, i } of pairs) key[k] = row[i]?.null ? null : (row[i]?.text ?? null);
    return key;
  };
  const edits: RowEdit[] = [];
  for (const [ri, values] of Object.entries(updateBuffer.value)) {
    if (deleteRows.value.includes(Number(ri))) continue;
    edits.push({ kind: "update", key: keyOf(Number(ri)), values });
  }
  for (const ri of deleteRows.value) edits.push({ kind: "delete", key: keyOf(ri) });
  for (const values of inserts) edits.push({ kind: "insert", values });
  if (!edits.length) {
    store.setStatus("没有待提交的变更", "info");
    return null;
  }
  return {
    ref: refObj.value,
    key_columns: pairs.map((x) => x.k),
    edits,
    transaction: true,
  };
}

async function preview() {
  const req = buildEditRequest();
  if (!req) return;
  try {
    const stmts = await api.previewDataEdit(props.tab.conn, req);
    previewSQL.value = stmts.join("\n");
    previewed.value = true;
    store.setStatus(`生成 ${stmts.length} 条变更语句`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`预览失败：${error.value}`, "error");
  }
}

async function apply() {
  const req = buildEditRequest();
  if (!req) return;
  if (!previewed.value) {
    const ok = await store.confirm({
      title: "尚未预览变更",
      message: "建议先点击「预览」检查生成的语句，确认无误后再应用到数据库。仍要直接应用吗？",
      danger: true,
      confirmText: "直接应用",
    });
    if (!ok) return;
  }
  applying.value = true;
  try {
    const res = await api.applyDataEdit(props.tab.conn, req);
    store.setStatus(`已应用 ${res.applied} 条变更`, "ok");
    await refresh();
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`应用失败：${error.value}`, "error");
  } finally {
    applying.value = false;
  }
}
</script>

<template>
  <div class="tdv">
    <div class="toolbar">
      <Icon name="table" :size="15" />
      <strong>{{ refObj.name }}</strong>
      <span class="badge">{{ props.tab.conn }}</span>
      <span v-if="refObj.database" class="badge">{{ refObj.database }}</span>
      <span class="divider-v"></span>
      <button
        class="btn btn-sm"
        :class="{ active: !store.leftCollapsed }"
        :title="store.leftCollapsed ? '显示左侧资源管理器' : '隐藏左侧资源管理器'"
        @click="store.leftCollapsed = !store.leftCollapsed"
      >
        <Icon name="panel-left" :size="13" /> {{ store.leftCollapsed ? "显示连接树" : "连接树" }}
      </button>
      <button class="btn" :disabled="loading" title="重新加载数据" @click="refresh">
        <Icon name="refresh" :size="13" /> 刷新
      </button>
      <button class="btn" :class="{ active: showFilters }" @click="showFilters = !showFilters">
        <Icon name="filter" :size="13" /> 过滤 / 排序
        <span v-if="conditions.length + sorts.length" class="badge badge-accent">{{ conditions.length + sorts.length }}</span>
      </button>
      <span class="divider-v"></span>
      <button class="btn btn-icon" :disabled="page <= 1" title="上一页" @click="goPage(-1)">
        <Icon name="chevron-left" :size="14" />
      </button>
      <span class="nowrap dim">第 {{ page }} 页</span>
      <button class="btn btn-icon" :disabled="!canNext" title="下一页" @click="goPage(1)">
        <Icon name="chevron-right" :size="14" />
      </button>
      <span class="dim" style="font-size: 11px">每页</span>
      <input type="number" :value="maxRows" style="width: 72px" min="10" max="5000" @change="onPageSizeChange" />
      <span class="grow"></span>
      <span v-if="effectiveKeys.length" class="badge badge-info" title="用于定位与回写数据的键列">
        <Icon name="key" :size="11" /> {{ effectiveKeys.join(", ") }}
      </span>
      <span v-else class="badge badge-warn">未找到主键，编辑受限</span>
      <span class="badge">{{ loadedRows }} 行</span>
    </div>

    <div v-if="showFilters" class="filter-card card">
      <div class="card-head">
        <Icon name="filter" :size="14" />
        <h4>过滤条件</h4>
        <span class="grow"></span>
        <button class="btn btn-sm" @click="addCond"><Icon name="plus" :size="12" /> 添加条件</button>
      </div>
      <div class="card-body col" style="gap: 6px">
        <div v-if="!conditions.length" class="faint" style="font-size: 11px">无条件 · 返回全部数据（受每页行数限制）</div>
        <div v-for="(c, i) in conditions" :key="'c' + i" class="row" style="gap: 6px">
          <select v-if="i > 0" v-model="c.join" style="width: 78px">
            <option value="AND">AND</option><option value="OR">OR</option>
          </select>
          <span v-else style="width: 78px" class="dim">WHERE</span>
          <select v-model="c.column" style="min-width: 120px">
            <option v-for="n in colNames" :key="n" :value="n">{{ n }}</option>
          </select>
          <select v-model="c.operator" style="width: 88px">
            <option>=</option><option>!=</option><option>&gt;</option><option>&lt;</option>
            <option>&gt;=</option><option>&lt;=</option><option>LIKE</option><option>IN</option>
          </select>
          <input v-model="c.value" class="grow" placeholder="值（留空忽略该条件）" />
          <button class="btn btn-icon" title="移除条件" @click="conditions.splice(i, 1)">
            <Icon name="close" :size="13" />
          </button>
        </div>
        <div class="divider-h"></div>
        <div class="row">
          <Icon name="sort" :size="14" />
          <h4 style="font-size: 12px">排序</h4>
          <span class="grow"></span>
          <button class="btn btn-sm" @click="addSort"><Icon name="plus" :size="12" /> 添加排序</button>
        </div>
        <div v-if="!sorts.length" class="faint" style="font-size: 11px">不排序 · 使用数据库默认顺序</div>
        <div v-for="(s, i) in sorts" :key="'s' + i" class="row" style="gap: 6px">
          <span class="dim" style="width: 78px">ORDER BY</span>
          <select v-model="s.column" style="min-width: 120px">
            <option v-for="n in colNames" :key="n" :value="n">{{ n }}</option>
          </select>
          <label class="check-line"><input type="checkbox" v-model="s.desc" /> 降序 (DESC)</label>
          <button class="btn btn-icon" title="移除排序" @click="sorts.splice(i, 1)">
            <Icon name="close" :size="13" />
          </button>
        </div>
        <div class="row" style="margin-top: 4px">
          <button class="btn btn-primary btn-sm" @click="page = 1; refresh()">
            <Icon name="search" :size="12" /> 应用条件
          </button>
          <button class="btn btn-sm" @click="clearFilters">清空条件</button>
          <span class="grow"></span>
          <span class="faint" style="font-size: 11px">列选项来自最近一次加载的结果集</span>
        </div>
      </div>
    </div>

    <div class="toolbar edit-bar">
      <button class="btn btn-sm" :disabled="!colNames.length" title="在表格下方增加一行空行" @click="addDraftRow">
        <Icon name="plus" :size="13" /> 新增行
        <span v-if="drafts.length" class="badge badge-accent">{{ drafts.length }}</span>
      </button>
      <button class="btn btn-sm btn-danger" :disabled="!selectedRows.length" title="删除选中的行" @click="markDeleted">
        <Icon name="trash" :size="13" /> 删除选中 ({{ selectedRows.length }})
      </button>
      <button v-if="deleteRows.length" class="btn btn-sm" @click="unmarkDeleted">撤销删除标记</button>
      <span class="divider-v"></span>
      <button class="btn btn-sm" :disabled="!selectedRows.length" title="将选中行复制到本页剪贴板" @click="copySelectedRows">
        <Icon name="copy" :size="13" /> 复制选中 ({{ selectedRows.length }})
      </button>
      <button class="btn btn-sm" :disabled="!copiedRows.length" title="把已复制的行作为新记录插入" @click="insertCopiedRows">
        <Icon name="paste" :size="13" /> 插入复制的行 ({{ copiedRows.length }})
      </button>
      <span class="divider-v"></span>
      <button class="btn btn-sm" :disabled="!pendingCount" @click="preview">
        <Icon name="eye" :size="13" /> 预览变更
      </button>
      <button class="btn btn-sm btn-primary" :disabled="!pendingCount || applying" @click="apply">
        <span v-if="applying" class="spinner" style="width: 12px; height: 12px"></span>
        <Icon v-else name="save" :size="13" /> 应用变更
      </button>
      <button class="btn btn-sm" :disabled="!pendingCount" @click="clearPending">放弃修改</button>
      <span class="grow"></span>
      <span class="badge" :class="pendingCount ? 'badge-warn' : ''">待提交 {{ pendingCount }} 项</span>
      <span class="faint" style="font-size: 11px">双击单元格编辑 · 勾选行可删除 · 新增行在表格下方空行填写（留空不提供，NULL 为空值）· 右键可复制 / 插入复制行</span>
    </div>

    <div v-if="previewSQL" class="preview-box">
      <div class="row" style="margin-bottom: 6px">
        <Icon name="code" :size="14" />
        <strong style="font-size: 12px">变更预览（只读，参数化语句）</strong>
        <span class="grow"></span>
        <button class="btn btn-sm" title="复制语句" @click="copyPreview">
          <Icon name="copy" :size="12" /> 复制
        </button>
        <button class="btn btn-icon" style="width: 22px; height: 22px" @click="previewSQL = ''; previewed = false">
          <Icon name="close" :size="12" />
        </button>
      </div>
      <pre class="code">{{ previewSQL }}</pre>
    </div>

    <div v-if="error" class="alert alert-error" style="margin: 0 10px 8px">
      <Icon name="alert" :size="15" />
      <span class="grow" style="white-space: pre-wrap">{{ error }}</span>
      <button class="btn btn-icon" style="width: 20px; height: 20px" @click="error = ''"><Icon name="close" :size="12" /></button>
    </div>

    <div class="grid-pane" @contextmenu.prevent="openGridMenu($event)">
      <ResultGrid
        :outcome="outcome"
        :loading="loading"
        :editable="true"
        :edits="updateBuffer"
        :deleted-rows="deleteRows"
        :selected-rows="selectedRows"
        :draft-rows="drafts"
        @celledit="onCellEdit"
        @rowselection="selectedRows = $event"
        @draftedit="onDraftEdit"
        @draftremove="removeDraft"
      />
    </div>

    <ContextMenu :open="ctxMenu.open" :x="ctxMenu.x" :y="ctxMenu.y" :items="ctxMenu.items" @close="ctxMenu.open = false" />
  </div>
</template>

<style scoped>
.tdv { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; padding: 8px; gap: 8px; overflow: hidden; }
.tdv > .toolbar { border: 1px solid var(--border); border-radius: var(--radius); padding: 7px 9px; max-width: 100%; }
.filter-card { flex-shrink: 0; }
.filter-card .card-body { padding: 10px; }
.edit-bar { padding: 6px 9px; }
.preview-box { flex-shrink: 0; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 9px 11px; }
.preview-box pre { max-height: 150px; overflow: auto; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 9px; font-size: 11px; line-height: 1.6; }
.grid-pane { flex: 1; min-height: 0; min-width: 0; display: flex; overflow: hidden; }
</style>
