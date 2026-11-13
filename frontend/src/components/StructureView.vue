<script setup lang="ts">
import { computed, onMounted, onActivated, ref, watch } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type {
  ColumnInfo,
  ColumnSpec,
  IndexInfo,
  IndexSpec,
  ObjectRef,
  SchemaChange,
  Script,
  TableSchema,
} from "@/types/domain";
import { COMMON_TYPES, CUSTOM_TYPE } from "@/config/columnTypes";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();
const refObj = computed<ObjectRef>(() => props.tab.ref!);

interface ColRow {
  original: ColumnInfo;
  spec: ColumnSpec;
  state: "keep" | "modify" | "rename" | "drop";
}
interface IdxRow {
  original: IndexInfo;
  state: "keep" | "drop";
}
interface NewIdx {
  name: string;
  columns: string;
  unique: boolean;
}
type EditField = "name" | "type" | "default" | "comment";

const schema = ref<TableSchema | null>(null);
const ddl = ref("");
const loading = ref(false);
const error = ref("");
const tab = ref<"columns" | "indexes" | "fks" | "ddl">("columns");
const busy = ref(false);

const colRows = ref<ColRow[]>([]);
const newColumns = ref<ColumnSpec[]>([]);
const indexRows = ref<IdxRow[]>([]);
const newIndexes = ref<NewIdx[]>([]);
const editing = ref<{ row: number; field: EditField } | null>(null);
const previewScript = ref<Script | null>(null);
const previewText = ref("");

const vFocus = { mounted: (el: HTMLElement) => el.focus() };

function toSpec(c: ColumnInfo): ColumnSpec {
  return {
    name: c.name,
    type: c.raw_type || c.data_type || "VARCHAR(255)",
    nullable: c.nullable,
    default: c.has_default ? c.default : undefined,
    comment: c.comment,
    auto_increment: c.auto_increment,
    primary_key: c.primary_key,
  };
}
function newCol(): ColumnSpec {
  return { name: "", type: "VARCHAR(255)", nullable: true, primary_key: false, auto_increment: false };
}
function newIdx(): NewIdx {
  return { name: "", columns: "", unique: false };
}

const typeOptions = computed(() => {
  const list = [...COMMON_TYPES];
  for (const r of colRows.value) {
    const t = r.spec.type;
    if (t && t !== CUSTOM_TYPE && !list.includes(t)) list.push(t);
  }
  return list;
});
// 字段类型下拉框分组：常用类型优先，其余为当前表已用的类型，便于选择。
const typeGroups = computed(() => {
  const extra = typeOptions.value.filter((t) => !COMMON_TYPES.includes(t) && t !== CUSTOM_TYPE);
  const groups = [{ label: "常用类型", options: COMMON_TYPES }];
  if (extra.length) groups.push({ label: "当前表其他类型", options: extra });
  return groups;
});

function resetChanges(s: TableSchema | null) {
  colRows.value = (s?.columns ?? []).map((c) => ({ original: c, spec: toSpec(c), state: "keep" }));
  indexRows.value = (s?.indexes ?? []).map((i) => ({ original: i, state: "keep" }));
  newColumns.value = [];
  newIndexes.value = [];
  editing.value = null;
  previewScript.value = null;
  previewText.value = "";
}

async function load(showCreate = false) {
  loading.value = true;
  error.value = "";
  try {
    const s = await api.describeObject(props.tab.conn, refObj.value);
    schema.value = s;
    resetChanges(s);
    ddl.value = showCreate ? await api.showCreateObject(props.tab.conn, refObj.value) : (s.ddl ?? "");
    store.setStatus(`${refObj.value.name} 结构已加载`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`结构加载失败：${error.value}`, "error");
  } finally {
    loading.value = false;
  }
}
onMounted(() => load());
// 每次被激活（从其它页签切回 / 重新打开）都重新读取结构，确保是全新状态，
// 不会残留上一次未应用的结构修改。
onActivated(() => load());

// 任何结构改动都会使已生成的脚本失效，必须重新预览才能应用。
watch([colRows, newColumns, indexRows, newIndexes], () => {
  previewScript.value = null;
  previewText.value = "";
}, { deep: true });

function columnChanged(r: ColRow) {
  const o = toSpec(r.original);
  return (
    r.spec.name !== o.name ||
    r.spec.type !== o.type ||
    r.spec.nullable !== o.nullable ||
    !!r.spec.primary_key !== !!o.primary_key ||
    !!r.spec.auto_increment !== !!o.auto_increment ||
    (r.spec.default ?? "") !== (o.default ?? "") ||
    (r.spec.comment ?? "") !== (o.comment ?? "")
  );
}
function markModified(r: ColRow) {
  if (r.state === "drop") return;
  if (!columnChanged(r)) {
    r.state = "keep";
    return;
  }
  r.state = r.spec.name.trim() !== r.original.name ? "rename" : "modify";
}
function startEdit(row: number, field: EditField) {
  if (colRows.value[row]?.state === "drop") return;
  editing.value = { row, field };
}
function isEditing(row: number, field: EditField) {
  return editing.value?.row === row && editing.value.field === field;
}
function finishEdit(r: ColRow) {
  editing.value = null;
  if (!r.spec.name.trim()) r.spec.name = r.original.name;
  markModified(r);
}
async function onTypeChange(r: ColRow, e: Event) {
  const value = (e.target as HTMLSelectElement).value;
  if (value === CUSTOM_TYPE) {
    const t = await store.promptText({
      title: "自定义字段类型",
      message: "输入目标数据库的完整类型，例如 DECIMAL(12,4) 或 NVARCHAR(200)。",
      label: "类型",
      value: r.spec.type,
      confirmText: "确定",
    });
    r.spec.type = t?.trim() || r.original.raw_type || "VARCHAR(255)";
  }
  editing.value = null;
  markModified(r);
}
function toggleBool(r: ColRow, field: "nullable" | "primary_key" | "auto_increment") {
  if (r.state === "drop") return;
  r.spec[field] = !r.spec[field];
  markModified(r);
}
function toggleDropColumn(r: ColRow) {
  if (r.state === "drop") markModified(r);
  else r.state = "drop";
  editing.value = null;
}
function toggleDropIndex(r: IdxRow) {
  r.state = r.state === "drop" ? "keep" : "drop";
}
function stateLabel(s: ColRow["state"]) {
  return s === "keep" ? "—" : s === "drop" ? "删除" : s === "rename" ? "重命名" : "修改";
}
function nameChanged(r: ColRow) { return r.spec.name.trim() !== r.original.name; }
function otherChanged(r: ColRow) {
  const o = toSpec(r.original), s = r.spec;
  return (
    s.type !== o.type ||
    s.nullable !== o.nullable ||
    !!s.primary_key !== !!o.primary_key ||
    !!s.auto_increment !== !!o.auto_increment ||
    (s.default ?? "") !== (o.default ?? "") ||
    (s.comment ?? "") !== (o.comment ?? "")
  );
}
function addColumn() { newColumns.value.push(newCol()); }
function addIndex() { newIndexes.value.push(newIdx()); }
function discard() { resetChanges(schema.value); }

function buildChanges(): SchemaChange[] {
  const changes: SchemaChange[] = [];
  for (const r of colRows.value) {
    if (r.state === "keep") continue;
    const spec: ColumnSpec = {
      ...r.spec,
      name: r.spec.name.trim() || r.original.name,
      type: r.spec.type.trim() || r.original.raw_type || "VARCHAR(255)",
    };
    if (r.state === "drop") {
      changes.push({ kind: "drop_column", column: { ...spec, name: r.original.name } });
      continue;
    }
    // 按修改值自动确定操作：改名生成 rename，其余属性变化生成 modify；两者同时发生时
    // 生成两条，rename 在前以保证后续 modify 使用新列名（跨 MySQL / PostgreSQL / SQL Server）。
    if (nameChanged(r)) changes.push({ kind: "rename_column", old_name: r.original.name, column: spec });
    if (otherChanged(r)) changes.push({ kind: "modify_column", column: spec });
  }
  for (const c of newColumns.value) {
    if (!c.name.trim()) continue;
    changes.push({
      kind: "add_column",
      column: { ...c, name: c.name.trim(), type: c.type.trim() || "VARCHAR(255)" },
    });
  }
  for (const r of indexRows.value) {
    if (r.state === "drop") changes.push({ kind: "drop_index", index_name: r.original.name });
  }
  for (const i of newIndexes.value) {
    const cols = i.columns.split(",").map((s) => s.trim()).filter(Boolean);
    if (!i.name.trim() || !cols.length) continue;
    const spec: IndexSpec = { name: i.name.trim(), columns: cols, unique: i.unique };
    changes.push({ kind: "add_index", index: spec });
  }
  return changes;
}
const changeCount = computed(() => buildChanges().length);
// 把待应用变更标记到页签上，关闭页签时据此弹窗提示。
watch(changeCount, (n) => { if (props.tab) props.tab.dirty = n > 0; }, { immediate: true });

async function preview() {
  const changes = buildChanges();
  if (!changes.length) { store.setStatus("暂无结构变更", "warn"); return; }
  busy.value = true;
  error.value = "";
  try {
    const script = await api.previewAlterTable(props.tab.conn, refObj.value, changes);
    previewScript.value = script;
    previewText.value = script.statements.join("\n") +
      (script.warnings?.length ? `\n\n-- 注意：${script.warnings.join("；")}` : "");
    store.setStatus(`已生成 ${script.statements.length} 条变更语句`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`SQL 预览失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

async function apply() {
  if (!changeCount.value) return;
  let script = previewScript.value;
  if (!script?.statements.length) {
    await preview();
    script = previewScript.value;
  }
  if (!script?.statements.length) return;
  const ok = await store.confirm({
    title: `应用表结构变更 · ${refObj.value.name}`,
    message: `将执行以下语句：\n${script.statements.join("\n")}${
      script.warnings?.length ? `\n\n注意：${script.warnings.join("；")}` : ""
    }`,
    danger: true,
    confirmText: "应用变更",
  });
  if (!ok) return;
  busy.value = true;
  try {
    await api.applyScript(props.tab.conn, script);
    store.setStatus("表结构已更新", "ok");
    store.invalidateObjects(props.tab.conn, refObj.value.database, refObj.value.schema);
    await load();
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`执行失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

async function copyDDL() {
  if (!ddl.value) { store.setStatus("DDL 为空", "warn"); return; }
  try {
    await navigator.clipboard.writeText(ddl.value);
    store.notify("DDL 已复制", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}
async function copyPreview() {
  if (!previewText.value) return;
  try {
    await navigator.clipboard.writeText(previewText.value);
    store.notify("变更 SQL 已复制", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

async function applyWithConfirm(title: string, warning: string, script: Script, confirmText: string, requireText?: string, subject?: string) {
  const ok = await store.confirm({
    title,
    message: `${warning}\n\n将执行以下语句：\n${script.statements.join("\n")}${
      (script.warnings?.length ?? 0) ? `\n\n注意：${script.warnings!.join("；")}` : ""
    }`,
    danger: true,
    confirmText,
    requireText,
    subject,
  });
  if (!ok) return false;
  busy.value = true;
  try {
    await api.applyScript(props.tab.conn, script);
    store.setStatus(`${title} 已执行`, "ok");
    store.invalidateObjects(props.tab.conn, refObj.value.database, refObj.value.schema);
    return true;
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`执行失败：${error.value}`, "error");
    return false;
  } finally {
    busy.value = false;
  }
}

async function dropObject() {
  try {
    const script = await api.previewDropObject(props.tab.conn, refObj.value);
    const ok = await applyWithConfirm(
      "删除对象",
      `将永久删除 ${refObj.value.database}.${refObj.value.name} 及其结构，此操作不可回滚。`,
      script,
      "删除对象",
      undefined,
      refObj.value.name,
    );
    if (ok) store.closeTab(props.tab.id);
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`删除失败：${error.value}`, "error");
  }
}

async function openDesigner() {
  store.openDesignerTab(props.tab.conn, refObj.value.database, refObj.value);
}
async function rename() {
  const name = await store.promptText({
    title: "重命名对象",
    message: `当前名称：${refObj.value.name}`,
    label: "新名称",
    value: refObj.value.name,
    confirmText: "重命名",
  });
  if (!name || name === refObj.value.name) return;
  try {
    const script = await api.previewRenameTable(props.tab.conn, refObj.value, name);
    const ok = await applyWithConfirm("重命名对象", `把 ${refObj.value.name} 重命名为 ${name}。`, script, "重命名");
    if (!ok) return;
    store.renameObjectTabs(props.tab.conn, refObj.value, name);
    await load();
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`重命名失败：${error.value}`, "error");
  }
}
</script>

<template>
  <div class="sv">
    <div class="toolbar">
      <Icon name="column" :size="15" />
      <strong>{{ refObj.name }}</strong>
      <span class="badge">{{ refObj.database }}</span>
      <span v-if="refObj.schema" class="badge">{{ refObj.schema }}</span>
      <span class="divider-v"></span>
      <button class="btn" :disabled="loading || busy" @click="load()">
        <Icon name="refresh" :size="13" /> 刷新
      </button>
      <button class="btn" @click="tab = 'ddl'; load(true)">
        <Icon name="code" :size="13" /> 查看建表 SQL
      </button>
      <button class="btn" :disabled="busy" @click="openDesigner">
        <Icon name="edit" :size="13" /> 修改结构
      </button>
      <button class="btn" :disabled="busy" @click="rename">
        <Icon name="edit" :size="13" /> 重命名
      </button>
      <button class="btn" @click="copyDDL" title="复制 DDL 到剪贴板">
        <Icon name="copy" :size="13" /> 复制 DDL
      </button>
      <span class="grow"></span>
      <span v-if="changeCount" class="badge badge-warn">待应用 {{ changeCount }} 项</span>
      <button class="btn btn-danger" :disabled="busy" @click="dropObject">
        <Icon name="trash" :size="13" /> 删除对象
      </button>
    </div>

    <div v-if="error" class="alert alert-error">
      <Icon name="alert" :size="15" />
      <span class="grow" style="white-space: pre-wrap">{{ error }}</span>
      <button class="btn btn-icon" style="width: 20px; height: 20px" @click="error = ''"><Icon name="close" :size="12" /></button>
    </div>

    <div v-if="loading" class="loading-line" style="padding: 10px"><span class="spinner"></span> 正在读取结构…</div>

    <template v-else-if="schema">
      <div class="meta-row">
        <span class="badge badge-info">{{ colRows.length + newColumns.length }} 个字段</span>
        <span class="badge">{{ indexRows.length + newIndexes.length }} 个索引</span>
        <span class="badge">{{ (schema.foreign_keys || []).length }} 个外键</span>
        <span v-if="schema.engine" class="badge">引擎 {{ schema.engine }}</span>
        <span v-if="schema.charset" class="badge">{{ schema.charset }} / {{ schema.collation }}</span>
        <span v-if="schema.comment" class="dim truncate" :title="schema.comment">{{ schema.comment }}</span>
      </div>

      <div class="subtabs">
        <button class="subtab" :class="{ active: tab === 'columns' }" @click="tab = 'columns'">字段</button>
        <button class="subtab" :class="{ active: tab === 'indexes' }" @click="tab = 'indexes'">索引</button>
        <button class="subtab" :class="{ active: tab === 'fks' }" @click="tab = 'fks'">外键</button>
        <button class="subtab" :class="{ active: tab === 'ddl' }" @click="tab = 'ddl'">DDL</button>
      </div>

      <div class="sv-body scroll">
        <template v-if="tab === 'columns'">
          <div class="row sv-tools">
            <button class="btn btn-sm" @click="addColumn"><Icon name="plus" :size="12" /> 新增字段</button>
            <span class="faint" style="font-size: 11px">双击单元格可就地修改；字段类型用下拉框选择，布尔列双击切换</span>
          </div>
          <div class="scroll sv-table">
            <table class="data-table">
              <thead>
                <tr><th>#</th><th>列名</th><th>类型</th><th>可空</th><th>主键</th><th>自增</th><th>默认值</th><th>注释</th><th>状态</th><th style="width: 34px"></th></tr>
              </thead>
              <tbody>
                <tr v-for="(r, i) in colRows" :key="r.original.name + i" :class="{ 'row-dropped': r.state === 'drop' }">
                  <td class="num">{{ r.original.position }}</td>
                  <td class="editable-cell" title="双击修改列名" @dblclick="startEdit(i, 'name')">
                    <input v-if="isEditing(i, 'name')" v-model="r.spec.name" v-focus class="cell-input" @blur="finishEdit(r)" @keydown.enter="finishEdit(r)" />
                    <span v-else class="mono"><strong>{{ r.spec.name }}</strong></span>
                  </td>
                  <td class="editable-cell" title="双击选择字段类型" @dblclick="startEdit(i, 'type')">
                    <select v-if="isEditing(i, 'type')" v-model="r.spec.type" v-focus class="cell-input" @change="onTypeChange(r, $event)" @blur="finishEdit(r)">
                      <optgroup v-for="g in typeGroups" :key="g.label" :label="g.label">
                        <option v-for="t in g.options" :key="t" :value="t">{{ t }}</option>
                      </optgroup>
                      <option :value="CUSTOM_TYPE">自定义类型…</option>
                    </select>
                    <span v-else class="mono">{{ r.spec.type }}</span>
                  </td>
                  <td class="editable-cell" title="双击切换" @dblclick="toggleBool(r, 'nullable')">
                    <span class="badge" :class="r.spec.nullable ? '' : 'badge-accent'">{{ r.spec.nullable ? "YES" : "NO" }}</span>
                  </td>
                  <td class="editable-cell" title="双击切换" @dblclick="toggleBool(r, 'primary_key')">
                    <span v-if="r.spec.primary_key" class="badge badge-accent">PK</span>
                    <span v-else class="dim">—</span>
                  </td>
                  <td class="editable-cell" title="双击切换" @dblclick="toggleBool(r, 'auto_increment')">
                    <span v-if="r.spec.auto_increment" class="badge badge-info">AUTO</span>
                    <span v-else class="dim">—</span>
                  </td>
                  <td class="editable-cell" title="双击修改默认值" @dblclick="startEdit(i, 'default')">
                    <input v-if="isEditing(i, 'default')" v-model="r.spec.default" v-focus class="cell-input" placeholder="留空 / NULL" @blur="finishEdit(r)" @keydown.enter="finishEdit(r)" />
                    <span v-else class="dim mono">{{ r.spec.default || "—" }}</span>
                  </td>
                  <td class="editable-cell" title="双击修改注释" @dblclick="startEdit(i, 'comment')">
                    <input v-if="isEditing(i, 'comment')" v-model="r.spec.comment" v-focus class="cell-input" placeholder="可选" @blur="finishEdit(r)" @keydown.enter="finishEdit(r)" />
                    <span v-else class="dim">{{ r.spec.comment || "—" }}</span>
                  </td>
                  <td><span class="badge" :class="r.state === 'drop' ? 'badge-danger' : r.state === 'keep' ? '' : 'badge-warn'">{{ stateLabel(r.state) }}</span></td>
                  <td>
                    <button class="btn btn-icon" :title="r.state === 'drop' ? '取消删除该字段' : '删除该字段'" @click="toggleDropColumn(r)">
                      <Icon :name="r.state === 'drop' ? 'refresh' : 'trash'" :size="12" />
                    </button>
                  </td>
                </tr>
                <tr v-for="(c, i) in newColumns" :key="'new' + i" class="row-added">
                  <td class="num">+</td>
                  <td><input v-model="c.name" class="cell-input" placeholder="新字段名" /></td>
                  <td>
                    <select v-model="c.type" class="cell-input">
                      <optgroup v-for="g in typeGroups" :key="g.label" :label="g.label">
                        <option v-for="t in g.options" :key="t" :value="t">{{ t }}</option>
                      </optgroup>
                    </select>
                  </td>
                  <td><input type="checkbox" v-model="c.nullable" /></td>
                  <td><input type="checkbox" v-model="c.primary_key" /></td>
                  <td><input type="checkbox" v-model="c.auto_increment" /></td>
                  <td><input v-model="c.default" class="cell-input" placeholder="可选" /></td>
                  <td><input v-model="c.comment" class="cell-input" placeholder="可选" /></td>
                  <td><span class="badge badge-ok">新增</span></td>
                  <td><button class="btn btn-icon" title="移除" @click="newColumns.splice(i, 1)"><Icon name="close" :size="12" /></button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <EmptyState v-if="!colRows.length && !newColumns.length" icon="column" title="没有字段信息" :compact="true" />
        </template>

        <template v-else-if="tab === 'indexes'">
          <div class="row sv-tools">
            <button class="btn btn-sm" @click="addIndex"><Icon name="plus" :size="12" /> 新增索引</button>
            <span class="faint" style="font-size: 11px">索引列用逗号分隔；已有索引可标记删除</span>
          </div>
          <div class="scroll sv-table">
            <table class="data-table">
              <thead><tr><th>索引名</th><th>列</th><th>唯一</th><th>主键</th><th>类型</th><th>状态</th><th style="width: 34px"></th></tr></thead>
              <tbody>
                <tr v-for="r in indexRows" :key="r.original.name" :class="{ 'row-dropped': r.state === 'drop' }">
                  <td class="mono"><strong>{{ r.original.name }}</strong></td>
                  <td class="mono">{{ r.original.columns.join(", ") }}</td>
                  <td><span v-if="r.original.unique" class="badge badge-ok">UNIQUE</span></td>
                  <td><span v-if="r.original.primary" class="badge badge-accent">PRIMARY</span></td>
                  <td class="dim">{{ r.original.index_type || "—" }}</td>
                  <td><span class="badge" :class="r.state === 'drop' ? 'badge-danger' : ''">{{ r.state === "drop" ? "删除" : "—" }}</span></td>
                  <td>
                    <button class="btn btn-icon" :title="r.state === 'drop' ? '取消删除' : '删除该索引'" @click="toggleDropIndex(r)">
                      <Icon :name="r.state === 'drop' ? 'refresh' : 'trash'" :size="12" />
                    </button>
                  </td>
                </tr>
                <tr v-for="(i, n) in newIndexes" :key="'ni' + n" class="row-added">
                  <td><input v-model="i.name" class="cell-input" placeholder="idx_xxx" /></td>
                  <td><input v-model="i.columns" class="cell-input" placeholder="col1, col2" /></td>
                  <td><input type="checkbox" v-model="i.unique" /></td>
                  <td class="dim">—</td>
                  <td class="dim">—</td>
                  <td><span class="badge badge-ok">新增</span></td>
                  <td><button class="btn btn-icon" title="移除" @click="newIndexes.splice(n, 1)"><Icon name="close" :size="12" /></button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <EmptyState v-if="!indexRows.length && !newIndexes.length" icon="index" title="暂无索引" description="点击「新增索引」开始设计。" :compact="true" />
        </template>

        <template v-else-if="tab === 'fks'">
          <table class="data-table" v-if="(schema.foreign_keys || []).length">
            <thead><tr><th>约束名</th><th>本表列</th><th>引用表</th><th>引用列</th></tr></thead>
            <tbody>
              <tr v-for="f in schema.foreign_keys" :key="f.name">
                <td class="mono"><strong>{{ f.name }}</strong></td>
                <td class="mono">{{ f.columns.join(", ") }}</td>
                <td class="mono">{{ f.ref_database ? f.ref_database + "." : "" }}{{ f.ref_table }}</td>
                <td class="mono">{{ f.ref_columns.join(", ") }}</td>
              </tr>
            </tbody>
          </table>
          <EmptyState v-else icon="link" title="暂无外键" description="该对象没有声明外键约束。" :compact="true" />
        </template>

        <template v-else>
          <div class="ddl-box">
            <div class="row" style="margin-bottom: 8px">
              <Icon name="code" :size="14" />
              <strong style="font-size: 12px">建表语句</strong>
              <span class="grow"></span>
              <button class="btn btn-sm" @click="copyDDL"><Icon name="copy" :size="12" /> 复制</button>
            </div>
            <pre class="code">{{ ddl || "（该数据库未提供 DDL）" }}</pre>
          </div>
        </template>
      </div>

      <div v-if="changeCount" class="change-panel">
        <div class="row wrap" style="gap: 6px">
          <Icon name="code" :size="13" />
          <strong style="font-size: 12px">待应用变更 {{ changeCount }} 项</strong>
          <span class="grow"></span>
          <button class="btn btn-sm" :disabled="busy" @click="preview"><Icon name="eye" :size="12" /> 生成 SQL 预览</button>
          <button class="btn btn-sm" :disabled="!previewText" @click="copyPreview"><Icon name="copy" :size="12" /> 复制</button>
          <button class="btn btn-sm" :disabled="busy" @click="discard"><Icon name="close" :size="12" /> 放弃修改</button>
          <button class="btn btn-sm btn-primary" :disabled="busy" @click="apply"><Icon name="save" :size="12" /> 应用变更</button>
        </div>
        <pre v-if="previewText" class="code change-preview">{{ previewText }}</pre>
        <span v-else class="field-hint">先「生成 SQL 预览」查看将要执行的 DDL，确认无误后再「应用变更」。</span>
      </div>
    </template>

    <EmptyState v-else icon="column" title="无法读取结构" description="请检查连接状态或该对象是否仍存在。" :compact="true">
      <button class="btn btn-sm" @click="load()">重试</button>
    </EmptyState>
  </div>
</template>

<style scoped>
.sv { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; padding: 8px; gap: 8px; overflow: hidden; }
.sv > .toolbar { border: 1px solid var(--border); border-radius: var(--radius); padding: 7px 9px; flex-shrink: 0; }
.meta-row { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; flex-shrink: 0; }
.sv-body { flex: 1; min-height: 0; min-width: 0; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); overflow: auto; }
.sv-tools { padding: 6px 8px; gap: 8px; flex-wrap: wrap; }
.sv-table { max-height: 340px; border-top: 1px solid var(--border); min-width: 0; }
.editable-cell { cursor: text; }
.editable-cell:hover { background: var(--accent-soft); }
.cell-input { width: 100%; min-width: 0; padding: 3px 6px; font-size: 11px; }
.row-dropped td { opacity: .55; text-decoration: line-through; }
.row-added { background: var(--accent-soft); }
.change-panel { flex-shrink: 0; max-height: 220px; overflow: auto; border: 1px solid var(--accent); border-radius: var(--radius); padding: 8px 10px; background: var(--surface-2); }
.change-preview { margin: 8px 0 0; padding: 8px; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); font-size: 11px; line-height: 1.6; white-space: pre-wrap; max-height: 140px; overflow: auto; }
.ddl-box { padding: 12px; }
.ddl-box pre { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 12px; font-size: 11px; line-height: 1.7; overflow: auto; white-space: pre-wrap; word-break: break-word; }
</style>
