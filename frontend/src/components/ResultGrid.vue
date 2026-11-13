<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref } from "vue";
import type { Cell, QueryOutcome } from "@/types/domain";
import { useWorkspace } from "@/stores/workspace";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = withDefaults(
  defineProps<{
    outcome: QueryOutcome | null;
    editable?: boolean;
    loading?: boolean;
    edits?: Record<number, Record<string, string | null>>;
    deletedRows?: number[];
    selectedRows?: number[];
    /** 待新增的空行，直接渲染在现有数据行下方就地填写。 */
    draftRows?: Record<string, string>[];
  }>(),
  { editable: false, loading: false },
);

const emit = defineEmits<{
  (e: "celledit", p: { rowIndex: number; column: string; value: string; isNull: boolean }): void;
  (e: "rowselection", rows: number[]): void;
  (e: "draftedit", p: { rowIndex: number; column: string; value: string }): void;
  (e: "draftremove", rowIndex: number): void;
  (e: "rowdelete", index: number): void;
}>();

const store = useWorkspace();
const pages = computed(() => props.outcome?.pages ?? []);
const activePage = ref(0);
const showJson = ref(false);
const sortState = ref<{ col: number; desc: boolean } | null>(null);
const selectedCell = ref<{ text: string; column: string; ri: number } | null>(null);
const editing = ref<{ pi: number; ri: number; ci: number; column: string } | null>(null);
const draft = ref("");

const page = computed(() => pages.value[Math.min(activePage.value, Math.max(0, pages.value.length - 1))]);
// 空结果集时后端可能把 columns / rows 序列化为 null，统一兜底为数组。
const pageColumns = computed(() => page.value?.columns ?? []);
const pageRows = computed(() => page.value?.rows ?? []);
const pageDocs = computed(() => page.value?.json_documents ?? []);

function display(c: Cell): string {
  if (!c) return "";
  return c.null ? "NULL" : c.text;
}

// 优先展示已编辑（未提交）的值；未编辑时回退到原始单元格内容。
function effectiveDisplay(ri: number, ci: number, cell: Cell): string {
  const colName = pageColumns.value[ci]?.name ?? "";
  const override = props.edits?.[ri]?.[colName];
  if (override !== undefined) return override === null ? "NULL" : override;
  return display(cell);
}

const sortedRows = computed(() => {
  const rows = pageRows.value;
  const indexed = rows.map((cells, ri) => ({ cells, ri }));
  const s = sortState.value;
  if (!s) return indexed;
  const factor = s.desc ? -1 : 1;
  return [...indexed].sort((a, b) => {
    const av = display(a.cells[s.col]);
    const bv = display(b.cells[s.col]);
    const an = Number(av);
    const bn = Number(bv);
    if (av !== "" && bv !== "" && !Number.isNaN(an) && !Number.isNaN(bn)) return (an - bn) * factor;
    return av.localeCompare(bv) * factor;
  });
});

function toggleSort(ci: number) {
  const s = sortState.value;
  sortState.value = s?.col === ci ? (s.desc ? null : { col: ci, desc: true }) : { col: ci, desc: false };
}

// 列宽调整：以列名为键记录宽度，未设置时由表格自动布局。
const colWidths = ref<Record<string, number>>({});
const resizing = ref<{ name: string; startX: number; startW: number } | null>(null);

function colStyle(name: string) {
  const w = colWidths.value[name];
  return w ? { width: `${w}px`, minWidth: `${w}px` } : {};
}

function startResize(e: MouseEvent, name: string) {
  e.preventDefault();
  e.stopPropagation();
  const th = (e.currentTarget as HTMLElement).closest("th");
  const startW = th ? th.getBoundingClientRect().width : 120;
  resizing.value = { name, startX: e.clientX, startW };
  document.addEventListener("mousemove", onResizeMove);
  document.addEventListener("mouseup", onResizeUp);
}
function onResizeMove(e: MouseEvent) {
  const r = resizing.value;
  if (!r) return;
  e.preventDefault();
  const w = Math.max(48, Math.round(r.startW + (e.clientX - r.startX)));
  colWidths.value = { ...colWidths.value, [r.name]: w };
}
function onResizeUp() {
  resizing.value = null;
  document.removeEventListener("mousemove", onResizeMove);
  document.removeEventListener("mouseup", onResizeUp);
}
onUnmounted(() => {
  document.removeEventListener("mousemove", onResizeMove);
  document.removeEventListener("mouseup", onResizeUp);
});

function isEdited(ri: number, column: string) {
  return props.edits?.[ri] ? Object.prototype.hasOwnProperty.call(props.edits[ri], column) : false;
}
function isDeleted(ri: number) {
  return (props.deletedRows ?? []).includes(ri);
}
function isSelected(ri: number) {
  return (props.selectedRows ?? []).includes(ri);
}

function toggleRow(ri: number) {
  const current = props.selectedRows ?? [];
  emit("rowselection", current.includes(ri) ? current.filter((x) => x !== ri) : [...current, ri]);
}

function selectCell(ri: number, ci: number, cell: Cell, column: string) {
  selectedCell.value = { text: effectiveDisplay(ri, ci, cell), column, ri };
}

async function startEdit(pi: number, ri: number, ci: number, column: string, cell: Cell) {
  if (!props.editable || isDeleted(ri)) return;
  editing.value = { pi, ri, ci, column };
  const initial = effectiveDisplay(ri, ci, cell);
  draft.value = initial === "NULL" ? "" : initial;
  await nextTick();
  // v-for 中的模板 ref 会是数组，直接取当前唯一的编辑框更可靠。
  const el = document.querySelector<HTMLInputElement>(".cell-input");
  el?.focus();
  el?.select();
}

function commitEdit() {
  const e = editing.value;
  if (!e) return;
  const isNull = draft.value.trim().toUpperCase() === "NULL";
  emit("celledit", {
    rowIndex: e.ri,
    column: e.column,
    value: isNull ? "" : draft.value.replace(/\r?\n/g, " "),
    isNull,
  });
  editing.value = null;
}
function cancelEdit() {
  editing.value = null;
}

function onDraftInput(rowIndex: number, column: string, e: Event) {
  emit("draftedit", { rowIndex, column, value: (e.target as HTMLInputElement).value });
}

function asText(): string {
  return pageRows.value
    .map((row) => row.map((c) => (c?.null ? "" : (c?.text ?? ""))).join("\t"))
    .join("\n");
}
function asCSV(): string {
  const esc = (v: string) => `"${v.replace(/"/g, '""')}"`;
  const cols = pageColumns.value.map((c) => esc(c.name)).join(",");
  const body = pageRows.value.map((row) => row.map((c) => esc(c?.null ? "" : (c?.text ?? ""))).join(",")).join("\n");
  return `${cols}\n${body}`;
}
async function copyText(text: string, label: string) {
  try {
    await navigator.clipboard.writeText(text);
    store.notify(`${label}已复制到剪贴板`, "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用，请手动选择内容复制", "warn");
  }
}
function exportCSV() {
  const blob = new Blob(["\uFEFF" + asCSV()], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "db-fly-result.csv";
  a.click();
  URL.revokeObjectURL(url);
  store.notify("已导出 CSV", "ok", 2000);
}

defineExpose({ copyText, asText });
</script>

<template>
  <div class="grid card">
    <div class="grid-bar">
      <div class="subtabs" v-if="pages.length > 1" style="border: 0">
        <button
          v-for="(p, i) in pages"
          :key="i"
          class="subtab"
          :class="{ active: activePage === i }"
          @click="activePage = i; showJson = false; sortState = null; selectedCell = null"
        >
          <Icon name="table" :size="12" /> 结果 {{ i + 1 }}
          <span class="badge" style="margin-left: 4px">{{ (p.rows || []).length }}</span>
        </button>
      </div>
      <span v-else class="section-title" style="margin: 0">结果集</span>

      <span class="grow"></span>
      <button
        v-if="page && pageDocs.length"
        class="btn btn-sm"
        :class="{ active: showJson }"
        title="查看 JSON 文档"
        @click="showJson = !showJson"
      >
        <Icon name="code" :size="13" /> JSON 文档 ({{ pageDocs.length }})
      </button>
      <button v-if="page && !showJson" class="btn btn-sm" title="复制结果为制表符文本" @click="copyText(asText(), '结果')">
        <Icon name="copy" :size="13" /> 复制
      </button>
      <button v-if="page && !showJson" class="btn btn-sm" title="导出为 CSV 文件" @click="exportCSV">
        <Icon name="download" :size="13" /> 导出 CSV
      </button>
    </div>

    <div v-if="loading" class="empty" style="height: 100%">
      <span class="spinner" style="width: 22px; height: 22px"></span>
      <p>正在执行查询…</p>
    </div>

    <EmptyState
      v-else-if="!outcome || !pages.length"
      icon="table"
      title="尚无查询结果"
      description="编写语句后点击「执行」，结果会显示在这里。支持多结果集分页查看。"
      :compact="true"
    />

    <template v-else>
      <div v-if="showJson" class="scroll grow json-view">
        <div v-for="(doc, i) in pageDocs" :key="i" class="doc-block">
          <div v-if="editable" class="doc-head">
            <span class="dim mono"># {{ i + 1 }}</span>
            <span class="grow"></span>
            <button class="btn btn-sm btn-danger" @click="emit('rowdelete', i)">
              <Icon name="trash" :size="12" /> 删除
            </button>
          </div>
          <pre class="code">{{ doc }}</pre>
        </div>
      </div>

      <div v-else class="scroll grow grid-scroll">
        <table class="data-table">
          <thead>
            <tr>
              <th v-if="editable" style="width: 38px; text-align: center">选</th>
              <th class="rownum">#</th>
              <th
                v-for="(col, ci) in pageColumns"
                :key="ci"
                class="sortable"
                :style="colStyle(col.name)"
                :title="`按 ${col.name} 排序`"
                @click="toggleSort(ci)"
              >
                <span class="row" style="gap: 5px">
                  {{ col.name }}
                  <Icon v-if="sortState?.col === ci" :name="sortState.desc ? 'chevron-down' : 'chevron-right'" :size="11" />
                </span>
                <span class="col-resizer" title="拖拽调整列宽" @mousedown.stop.prevent="startResize($event, col.name)" @click.stop></span>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="item in sortedRows"
              :key="item.ri"
              :class="{ 'row-deleted': isDeleted(item.ri), 'row-selected': isSelected(item.ri) }"
            >
              <td v-if="editable" style="text-align: center">
                <input type="checkbox" :checked="isSelected(item.ri)" @change="toggleRow(item.ri)" />
              </td>
              <td class="rownum dim">{{ item.ri + 1 }}</td>
              <td
                v-for="(cell, ci) in item.cells"
                :key="ci"
                class="cell"
                :style="colStyle(pageColumns[ci]?.name ?? '')"
                :class="{ null: cell?.null, edited: isEdited(item.ri, pageColumns[ci]?.name ?? ''), 'cell-active': selectedCell?.ri === item.ri && selectedCell?.column === pageColumns[ci]?.name }"
                :title="effectiveDisplay(item.ri, ci, cell)"
                @click="selectCell(item.ri, ci, cell, pageColumns[ci]?.name ?? '')"
                @dblclick="startEdit(activePage, item.ri, ci, pageColumns[ci]?.name ?? '', cell)"
              >
                <input
                  v-if="editing && editing.ri === item.ri && editing.ci === ci"
                  v-model="draft"
                  class="cell-input"
                  @keydown.enter.prevent="commitEdit"
                  @keydown.esc.prevent="cancelEdit"
                  @blur="commitEdit"
                  @click.stop
                />
                <span v-else class="cell-text">{{ effectiveDisplay(item.ri, ci, cell) }}</span>
              </td>
            </tr>
            <tr v-for="(d, di) in (draftRows ?? [])" :key="'draft' + di" class="row-draft">
              <td v-if="editable" style="text-align: center">
                <button class="btn btn-icon" style="width: 20px; height: 20px" title="移除该新增行" @click="emit('draftremove', di)">
                  <Icon name="close" :size="11" />
                </button>
              </td>
              <td class="rownum" style="color: var(--accent)">+</td>
              <td v-for="(col, ci) in pageColumns" :key="ci" class="cell" :style="colStyle(col.name)">
                <input
                  class="cell-input draft-input"
                  :value="d[col.name] ?? ''"
                  placeholder="留空 / NULL"
                  @input="onDraftInput(di, col.name, $event)"
                  @click.stop
                />
              </td>
            </tr>
            <tr v-if="!sortedRows.length && !(draftRows?.length)">
              <td :colspan="pageColumns.length + (editable ? 2 : 1)" class="dim" style="text-align: center; padding: 24px">
                查询成功，但没有返回数据行
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="selectedCell" class="cell-detail">
        <Icon name="eye" :size="13" />
        <span class="badge">{{ selectedCell.column }}</span>
        <span class="grow truncate mono" :title="selectedCell.text">{{ selectedCell.text || "（空）" }}</span>
        <button class="btn btn-sm" @click="copyText(selectedCell.text, '单元格')">
          <Icon name="copy" :size="12" /> 复制
        </button>
        <button class="btn btn-icon" style="width: 22px; height: 22px" title="关闭" @click="selectedCell = null">
          <Icon name="close" :size="12" />
        </button>
      </div>

      <div class="grid-foot dim">
        <span>{{ pageColumns.length }} 列 · {{ pageRows.length }} 行</span>
        <span class="divider-v"></span>
        <span v-if="outcome?.summary">影响 {{ outcome.summary.affected }} 行 · 耗时 {{ outcome.summary.duration_ms }} ms</span>
        <span v-if="outcome?.summary?.truncated" class="badge badge-warn">结果已截断</span>
        <span class="grow"></span>
        <span v-if="editable" class="faint">双击单元格编辑 · 输入 NULL 表示空值</span>
      </div>
      <div v-for="(m, i) in outcome?.summary?.messages || []" :key="i" class="grid-msg" :class="m.level === 'error' ? 'alert-error' : ''">
        [{{ m.level }}] {{ m.text }}
      </div>
    </template>
  </div>
</template>

<style scoped>
.grid { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; overflow: hidden; }
.grid-bar { display: flex; align-items: center; gap: 7px; padding: 6px 9px; border-bottom: 1px solid var(--border); background: var(--surface-2); flex-shrink: 0; flex-wrap: wrap; }
.grid-scroll { flex: 1; min-height: 0; min-width: 0; max-width: 100%; }
.rownum { width: 44px; text-align: right; color: var(--text-faint); font-family: var(--mono); font-size: var(--fs-xs); position: sticky; left: 0; background: var(--surface-2); z-index: 1; }
.sortable { cursor: pointer; user-select: none; }
.sortable:hover { color: var(--text); background: var(--surface-3); }
.data-table { width: 100%; }
.data-table thead th { position: sticky; top: 0; z-index: 2; background: var(--surface-2); }
.data-table thead th.rownum { left: 0; z-index: 3; }
.col-resizer { position: absolute; top: 0; right: 0; width: 7px; height: 100%; cursor: col-resize; user-select: none; z-index: 1; }
.col-resizer:hover { background: var(--accent); }
.cell { max-width: 420px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: default; }
.cell-text { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cell.edited { background: var(--warn-soft) !important; box-shadow: inset 2px 0 0 var(--warn); }
.cell.cell-active { outline: 1px solid var(--accent); outline-offset: -1px; }
.cell-input { width: 100%; min-width: 0; padding: 2px 4px; background: var(--bg); border-color: var(--accent); font-family: var(--mono); }
.row-deleted td { opacity: .45; text-decoration: line-through; background: var(--danger-soft) !important; }
.row-selected td { background: var(--accent-soft) !important; }
.row-draft td { background: var(--accent-soft) !important; }
.draft-input { background: var(--surface); }
.cell-detail { display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-top: 1px solid var(--border); background: var(--surface-2); flex-shrink: 0; font-size: var(--fs-xs); }
.grid-foot { display: flex; align-items: center; gap: 9px; padding: 6px 10px; border-top: 1px solid var(--border); font-size: var(--fs-xs); flex-shrink: 0; flex-wrap: wrap; }
.grid-msg { padding: 4px 10px; font-size: var(--fs-xs); color: var(--text-dim); border-top: 1px solid var(--border); }
.json-view { padding: 10px; }
.doc-block { margin-bottom: 10px; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; }
.doc-block .doc-head { display: flex; align-items: center; gap: 8px; padding: 5px 8px; background: var(--surface-2); border-bottom: 1px solid var(--border); }
.doc-block pre { margin: 0; border: 0; border-radius: 0; }
.json-view pre { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 10px; margin-bottom: 8px; font-size: var(--fs-xs); line-height: 1.6; overflow: auto; }
</style>
