<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type {
  ColumnInfo,
  ColumnSpec,
  IndexSpec,
  ObjectRef,
  SchemaChange,
  TableDefinition,
  TableSchema,
} from "@/types/domain";
import { COMMON_TYPES, CUSTOM_TYPE } from "@/config/columnTypes";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

function newCol(): ColumnSpec {
  return { name: "", type: "VARCHAR(255)", nullable: true, primary_key: false, auto_increment: false };
}
interface IndexRow { name: string; columns: string; unique: boolean }
function newIndexRow(): IndexRow { return { name: "", columns: "", unique: false }; }
interface AlterColumnRow {
  original: ColumnInfo;
  spec: ColumnSpec;
  dropped?: boolean;
}
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

const isAlter = computed(() => props.tab.mode === "alter");
const connName = ref(props.tab.conn);
const dbName = ref(props.tab.database ?? "");
const tableName = ref(props.tab.table ?? "");
const columns = ref<ColumnSpec[]>([newCol()]);
const indexRows = ref<IndexRow[]>([]);
const engine = ref("");
const charset = ref("");
const collation = ref("");
const comment = ref("");
const includeOptions = ref(false);

// 三个子 tab：字段 / 索引 / 表基础信息
const activeSub = ref<"fields" | "indexes" | "info">("fields");

const schema = ref<TableSchema | null>(null);
const alterRows = ref<AlterColumnRow[]>([]);
const addedColumns = ref<ColumnSpec[]>([]);

const preview = ref("");
const error = ref("");
const busy = ref(false);
const created = ref(false);
const loading = ref(false);

const databases = computed(() => store.dbCache[connName.value] || []);
const refObj = computed<ObjectRef>(() => ({ database: dbName.value, name: tableName.value }));
const canSubmit = computed(() => {
  if (!connName.value || !dbName.value.trim() || !tableName.value.trim()) return false;
  if (isAlter.value) return !!schema.value && busy.value === false;
  return columns.value.some((c) => c.name.trim());
});
const typeGroups = computed(() => {
  const used = new Set<string>();
  for (const r of alterRows.value) if (r.spec.type) used.add(r.spec.type);
  for (const c of columns.value) if (c.type) used.add(c.type);
  for (const c of addedColumns.value) if (c.type) used.add(c.type);
  const extra = [...used].filter((t) => !COMMON_TYPES.includes(t) && t !== CUSTOM_TYPE);
  const groups = [{ label: "常用类型", options: COMMON_TYPES }];
  if (extra.length) groups.push({ label: "当前表其他类型", options: extra });
  return groups;
});

function typeOf(c: ColumnInfo): string { return c.raw_type || c.data_type || "VARCHAR(255)"; }
function nameChanged(r: AlterColumnRow) { return r.spec.name.trim() !== r.original.name; }
function otherChanged(r: AlterColumnRow) {
  const o = r.original, s = r.spec;
  return (
    s.type !== typeOf(o) ||
    !!s.nullable !== !!o.nullable ||
    !!s.primary_key !== !!o.primary_key ||
    !!s.auto_increment !== !!o.auto_increment ||
    (s.default ?? "") !== (o.has_default ? (o.default ?? "") : "") ||
    (s.comment ?? "") !== (o.comment ?? "")
  );
}
function rowState(r: AlterColumnRow): "keep" | "modify" | "rename" | "drop" {
  if (r.dropped) return "drop";
  if (nameChanged(r)) return "rename";
  if (otherChanged(r)) return "modify";
  return "keep";
}
function stateText(r: AlterColumnRow) {
  if (r.dropped) return "删除";
  const bits: string[] = [];
  if (nameChanged(r)) bits.push("重命名");
  if (otherChanged(r)) bits.push("修改");
  return bits.length ? bits.join(" / ") : "—";
}
async function onTypePick(spec: ColumnSpec, e: Event) {
  const v = (e.target as HTMLSelectElement).value;
  if (v === CUSTOM_TYPE) {
    const t = await store.promptText({
      title: "自定义字段类型",
      message: "输入目标数据库的完整类型，例如 DECIMAL(12,4) 或 NVARCHAR(200)。",
      label: "类型",
      value: spec.type,
      confirmText: "确定",
    });
    spec.type = t?.trim() || "VARCHAR(255)";
  }
}

const changeCount = computed(() => buildChanges().length);
// 把待应用变更标记到页签上，关闭页签时据此弹窗提示。
watch(changeCount, (n) => { if (props.tab) props.tab.dirty = n > 0; }, { immediate: true });

// 是否存在可放弃的修改：alter 看待应用变更；create 看是否已填写字段 / 索引。
const hasChanges = computed(() => {
  if (isAlter.value) return changeCount.value > 0;
  return columns.value.some((c) => c.name.trim()) || indexRows.value.length > 0;
});

// 放弃修改：回到打开时的初始状态（alter 还原为已加载结构，create 清空为空白表）。
function discard() {
  error.value = "";
  preview.value = "";
  if (isAlter.value) {
    if (schema.value) {
      alterRows.value = (schema.value.columns ?? []).map((c) => ({
        original: c,
        spec: toSpec(c),
        dropped: false,
      }));
    }
    addedColumns.value = [];
    includeOptions.value = false;
  } else {
    columns.value = [newCol()];
    indexRows.value = [];
    engine.value = "";
    charset.value = "";
    collation.value = "";
    comment.value = "";
  }
}

onMounted(async () => {
  if (!isAlter.value) return;
  loading.value = true;
  try {
    const s = await api.describeObject(connName.value, refObj.value);
    schema.value = s;
    alterRows.value = (s.columns ?? []).map((c) => ({ original: c, spec: toSpec(c), dropped: false }));
    engine.value = s.engine ?? "";
    charset.value = s.charset ?? "";
    collation.value = s.collation ?? "";
    comment.value = s.comment ?? "";
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    loading.value = false;
  }
});

function validate(): boolean {
  error.value = "";
  if (!connName.value) { error.value = "请选择目标连接"; return false; }
  if (!dbName.value.trim()) { error.value = "请填写目标数据库"; return false; }
  if (!tableName.value.trim()) { error.value = "请填写表名"; return false; }
  if (!isAlter.value) {
    if (!columns.value.some((c) => c.name.trim())) { error.value = "至少需要一个字段"; return false; }
    const names = columns.value.filter((c) => c.name.trim()).map((c) => c.name.trim());
    if (new Set(names).size !== names.length) { error.value = "字段名不能重复"; return false; }
  } else if (!changeCount.value) {
    error.value = "尚未做任何结构变更"; return false;
  }
  return true;
}

function buildDefinition(): TableDefinition {
  const indexes: IndexSpec[] = indexRows.value
    .filter((r) => r.name.trim() && r.columns.trim())
    .map((r) => ({
      name: r.name.trim(),
      columns: r.columns.split(",").map((s) => s.trim()).filter(Boolean),
      unique: r.unique,
    }));
  return {
    ref: refObj.value,
    columns: columns.value
      .filter((c) => c.name.trim())
      .map((c) => ({ ...c, name: c.name.trim(), type: c.type.trim() || "VARCHAR(255)" })),
    indexes,
    engine: engine.value || undefined,
    charset: charset.value || undefined,
    collation: collation.value || undefined,
    comment: comment.value || undefined,
  };
}

function buildChanges(): SchemaChange[] {
  const changes: SchemaChange[] = [];
  for (const r of alterRows.value) {
    if (r.dropped) {
      changes.push({ kind: "drop_column", column: { ...r.spec, name: r.original.name } });
      continue;
    }
    const spec: ColumnSpec = {
      ...r.spec,
      name: r.spec.name.trim() || r.original.name,
      type: r.spec.type.trim() || typeOf(r.original),
    };
    const nc = nameChanged(r);
    const oc = otherChanged(r);
    // 按修改值自动确定操作：改名生成 rename（携带完整新定义），其余属性变化生成 modify；
    // 同时改名 + 改类型/属性时两条都生成，rename 在前以保证后续 modify 使用新列名。
    if (nc) changes.push({ kind: "rename_column", old_name: r.original.name, column: spec });
    if (oc) changes.push({ kind: "modify_column", column: spec });
  }
  for (const c of addedColumns.value) {
    if (!c.name.trim()) continue;
    changes.push({ kind: "add_column", column: { ...c, name: c.name.trim(), type: c.type.trim() || "VARCHAR(255)" } });
  }
  if (includeOptions.value) {
    changes.push({
      kind: "table_options",
      engine: engine.value || undefined,
      charset: charset.value || undefined,
      collation: collation.value || undefined,
      comment: comment.value || undefined,
    });
  }
  return changes;
}

async function generateScript() {
  return isAlter.value
    ? api.previewAlterTable(connName.value, refObj.value, buildChanges())
    : api.previewCreateTable(connName.value, buildDefinition());
}

async function doPreview() {
  if (!validate()) return;
  busy.value = true;
  try {
    const script = await generateScript();
    preview.value = script.statements.join("\n");
    if (script.warnings?.length) preview.value += `\n\n-- 注意：${script.warnings.join("；")}`;
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
}

async function submit() {
  if (!validate()) return;
  busy.value = true;
  try {
    const script = await generateScript();
    preview.value = script.statements.join("\n");
    const ok = await store.confirm({
      title: isAlter.value ? `修改表结构 · ${tableName.value.trim()}` : `创建表 · ${tableName.value.trim()}`,
      message: `将在 ${connName.value} / ${dbName.value} 执行：\n${script.statements.join("\n")}${
        (script.warnings?.length ?? 0) ? `\n\n注意：${script.warnings!.join("；")}` : ""
      }`,
      confirmText: isAlter.value ? "应用变更" : "创建表",
      danger: isAlter.value,
    });
    if (!ok) return;
    await api.applyScript(connName.value, script);
    store.invalidateObjects(connName.value, dbName.value);
    store.setStatus(isAlter.value ? `表 ${tableName.value} 结构已更新` : `表 ${tableName.value} 已创建`, "ok");
    error.value = "";
    created.value = !isAlter.value;
    if (isAlter.value) {
      const s = await api.describeObject(connName.value, refObj.value);
      schema.value = s;
      alterRows.value = (s.columns ?? []).map((c) => ({ original: c, spec: toSpec(c), dropped: false }));
      addedColumns.value = [];
      includeOptions.value = false;
      preview.value = "";
    }
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`${isAlter.value ? "修改" : "创建"}失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

function openStructure() {
  store.openStructureTab(connName.value, refObj.value);
}
function openData() {
  store.openDataTab(connName.value, refObj.value);
}
</script>

<template>
  <div class="designer">
    <div class="toolbar page-head">
      <Icon :name="isAlter ? 'edit' : 'table'" :size="15" />
      <strong>{{ isAlter ? "编辑表结构" : "新建表" }}</strong>
      <span class="badge">{{ connName }}</span>
      <span class="badge">{{ dbName || "未选择数据库" }}</span>
      <span v-if="tableName" class="badge badge-accent">{{ tableName }}</span>
      <span class="grow"></span>
      <button v-if="created" class="btn btn-sm" @click="openStructure"><Icon name="column" :size="13" /> 查看结构</button>
      <button v-if="created" class="btn btn-sm" @click="openData"><Icon name="table" :size="13" /> 浏览数据</button>
      <span v-if="isAlter" class="badge" :class="changeCount ? 'badge-warn' : ''">待应用 {{ changeCount }} 项变更</span>
    </div>

    <!-- 目标信息常驻，子 tab 切换不丢失定位 -->
    <div class="target-bar" v-if="!isAlter">
      <div class="field">
        <label class="field-label">连接 *</label>
        <select v-model="connName">
          <option v-for="c in store.connections" :key="c.name" :value="c.name">{{ c.name }}</option>
        </select>
      </div>
      <div class="field">
        <label class="field-label">目标数据库 *</label>
        <input v-model="dbName" list="designer-db" :disabled="isAlter" placeholder="数据库名" />
        <datalist id="designer-db"><option v-for="d in databases" :key="d.name" :value="d.name"></option></datalist>
      </div>
      <div class="field">
        <label class="field-label">表名 *</label>
        <input v-model="tableName" :disabled="isAlter" placeholder="例如 users" />
      </div>
    </div>

    <div class="subtabs">
      <button :class="{ active: activeSub === 'fields' }" @click="activeSub = 'fields'">
        <Icon name="column" :size="13" /> 字段
      </button>
      <button :class="{ active: activeSub === 'indexes' }" @click="activeSub = 'indexes'">
        <Icon name="key" :size="13" /> 索引
      </button>
      <button :class="{ active: activeSub === 'info' }" @click="activeSub = 'info'">
        <Icon name="info" :size="13" /> 表基础信息
      </button>
    </div>

    <div class="page-body scroll">
      <div v-if="loading" class="empty"><span class="spinner" style="width: 22px; height: 22px"></span><p>正在读取表结构…</p></div>
      <template v-else>
        <!-- 字段 -->
        <div v-show="activeSub === 'fields'" class="subpane">
          <template v-if="!isAlter">
            <div class="card">
              <div class="card-head">
                <Icon name="column" :size="14" /><h4>字段定义 ({{ columns.length }})</h4>
                <span class="grow"></span>
                <button class="btn btn-sm" @click="columns.push(newCol())"><Icon name="plus" :size="12" /> 添加字段</button>
              </div>
              <div class="card-body">
                <div class="scroll table-scroll">
                  <table class="data-table">
                    <thead>
                      <tr><th style="width: 30px">#</th><th>列名 *</th><th>类型 *</th><th>可空</th><th>主键</th><th>自增</th><th>默认值</th><th>注释</th><th style="width: 36px"></th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="(c, i) in columns" :key="i">
                        <td class="dim">{{ i + 1 }}</td>
                        <td><input v-model="c.name" placeholder="id" style="width: 130px" /></td>
                        <td>
                          <select v-model="c.type" style="width: 150px" @change="onTypePick(c, $event)">
                            <optgroup v-for="g in typeGroups" :key="g.label" :label="g.label">
                              <option v-for="t in g.options" :key="t" :value="t">{{ t }}</option>
                            </optgroup>
                            <option :value="CUSTOM_TYPE">自定义类型…</option>
                          </select>
                        </td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.nullable" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.primary_key" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.auto_increment" /></td>
                        <td><input v-model="c.default" placeholder="NULL" style="width: 100px" /></td>
                        <td><input v-model="c.comment" placeholder="可选" style="width: 110px" /></td>
                        <td><button class="btn btn-icon" :disabled="columns.length <= 1" title="删除该字段" @click="columns.splice(i, 1)"><Icon name="close" :size="12" /></button></td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <span class="field-hint">主键字段建议取消「可空」。字段类型从下拉框选择，或选「自定义类型…」输入方言类型。</span>
              </div>
            </div>
          </template>

          <template v-else>
            <div v-if="!schema" class="empty"><Icon name="alert" :size="22" /><p>无法读取表结构</p></div>
            <div v-else class="card">
              <div class="card-head">
                <Icon name="column" :size="14" /><h4>字段 ({{ alterRows.length + addedColumns.length }})</h4>
                <span class="grow"></span>
                <button class="btn btn-sm" @click="addedColumns.push(newCol())"><Icon name="plus" :size="12" /> 添加字段</button>
                <span class="faint" style="font-size: 11px">直接修改字段即可，操作类型自动判定</span>
              </div>
              <div class="card-body">
                <div class="scroll table-scroll">
                  <table class="data-table">
                    <thead>
                      <tr>
                        <th style="width: 30px">#</th>
                        <th>列名 *</th><th>类型 *</th><th>可空</th><th>主键</th><th>自增</th><th>默认值</th><th>注释</th>
                        <th>状态</th><th style="width: 34px"></th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(r, i) in alterRows" :key="r.original.name + i" :class="{ 'row-dropped': r.dropped }">
                        <td class="dim mono" :title="r.original.name">{{ i + 1 }}</td>
                        <td><input v-model="r.spec.name" :disabled="r.dropped" placeholder="列名" style="width: 130px" /></td>
                        <td>
                          <select v-model="r.spec.type" :disabled="r.dropped" style="width: 150px" @change="onTypePick(r.spec, $event)">
                            <optgroup v-for="g in typeGroups" :key="g.label" :label="g.label">
                              <option v-for="t in g.options" :key="t" :value="t">{{ t }}</option>
                            </optgroup>
                            <option :value="CUSTOM_TYPE">自定义类型…</option>
                          </select>
                        </td>
                        <td style="text-align: center"><input type="checkbox" v-model="r.spec.nullable" :disabled="r.dropped" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="r.spec.primary_key" :disabled="r.dropped" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="r.spec.auto_increment" :disabled="r.dropped" /></td>
                        <td><input v-model="r.spec.default" :disabled="r.dropped" placeholder="NULL" style="width: 100px" /></td>
                        <td><input v-model="r.spec.comment" :disabled="r.dropped" placeholder="可选" style="width: 110px" /></td>
                        <td>
                          <span class="badge" :class="rowState(r) === 'drop' ? 'badge-danger' : rowState(r) === 'keep' ? '' : 'badge-warn'">{{ stateText(r) }}</span>
                        </td>
                        <td>
                          <button class="btn btn-icon" :title="r.dropped ? '取消删除该字段' : '删除该字段'" @click="r.dropped = !r.dropped">
                            <Icon :name="r.dropped ? 'refresh' : 'trash'" :size="12" />
                          </button>
                        </td>
                      </tr>
                      <tr v-for="(c, i) in addedColumns" :key="'new' + i" class="row-added">
                        <td class="dim">+</td>
                        <td><input v-model="c.name" placeholder="新字段名" style="width: 130px" /></td>
                        <td>
                          <select v-model="c.type" style="width: 150px" @change="onTypePick(c, $event)">
                            <optgroup v-for="g in typeGroups" :key="g.label" :label="g.label">
                              <option v-for="t in g.options" :key="t" :value="t">{{ t }}</option>
                            </optgroup>
                            <option :value="CUSTOM_TYPE">自定义类型…</option>
                          </select>
                        </td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.nullable" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.primary_key" /></td>
                        <td style="text-align: center"><input type="checkbox" v-model="c.auto_increment" /></td>
                        <td><input v-model="c.default" placeholder="可选" style="width: 100px" /></td>
                        <td><input v-model="c.comment" placeholder="可选" style="width: 110px" /></td>
                        <td><span class="badge badge-ok">新增</span></td>
                        <td><button class="btn btn-icon" title="移除" @click="addedColumns.splice(i, 1)"><Icon name="close" :size="12" /></button></td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <span class="field-hint">修改列名 / 类型等会自动生成 RENAME / MODIFY 语句；列名留空时回退为原名。新增字段显示在表格下方的空行中。</span>
              </div>
            </div>
          </template>
        </div>

        <!-- 索引 -->
        <div v-show="activeSub === 'indexes'" class="subpane">
          <div v-if="!isAlter" class="card">
            <div class="card-head">
              <Icon name="key" :size="14" /><h4>索引 ({{ indexRows.length }})</h4>
              <span class="grow"></span>
              <button class="btn btn-sm" @click="indexRows.push(newIndexRow())"><Icon name="plus" :size="12" /> 添加索引</button>
            </div>
            <div class="card-body col" style="gap: 6px">
              <div v-for="(r, i) in indexRows" :key="i" class="row" style="gap: 6px">
                <input v-model="r.name" placeholder="索引名，如 idx_email" style="width: 200px" />
                <input v-model="r.columns" class="grow" placeholder="列名，多个用逗号分隔" />
                <label class="check-line nowrap"><input type="checkbox" v-model="r.unique" /> 唯一</label>
                <button class="btn btn-icon" title="删除索引" @click="indexRows.splice(i, 1)"><Icon name="close" :size="12" /></button>
              </div>
              <span v-if="!indexRows.length" class="field-hint">暂无索引，可添加普通或唯一索引。</span>
            </div>
          </div>

          <div v-else class="card">
            <div class="card-head"><Icon name="key" :size="14" /><h4>现有索引（只读）</h4></div>
            <div class="card-body">
              <ul v-if="schema?.indexes?.length" class="index-list">
                <li v-for="ix in schema.indexes" :key="ix.name">
                  <span class="mono">{{ ix.name }}</span>
                  <span class="faint">{{ ix.unique ? "唯一" : "普通" }} · {{ (ix.columns || []).join(", ") }}</span>
                </li>
              </ul>
              <EmptyState v-else :compact="true" icon="key" title="暂无索引" description="该表当前没有定义索引。" />
              <span class="field-hint">修改表结构的索引管理请使用「结构」页（在表数据/结构页双击进入）。</span>
            </div>
          </div>
        </div>

        <!-- 表基础信息 -->
        <div v-show="activeSub === 'info'" class="subpane">
          <div class="card">
            <div class="card-head">
              <Icon name="settings" :size="14" /><h4>表基础信息</h4>
              <span class="grow"></span>
              <label v-if="isAlter" class="check-line"><input type="checkbox" v-model="includeOptions" /> 应用表选项变更</label>
            </div>
            <div class="card-body form-grid three">
              <div class="field"><label class="field-label">存储引擎</label><input v-model="engine" :disabled="isAlter && !includeOptions" placeholder="如 InnoDB（可选）" /></div>
              <div class="field"><label class="field-label">字符集</label><input v-model="charset" :disabled="isAlter && !includeOptions" placeholder="如 utf8mb4（可选）" /></div>
              <div class="field"><label class="field-label">排序规则</label><input v-model="collation" :disabled="isAlter && !includeOptions" placeholder="可选" /></div>
              <div class="field span-2"><label class="field-label">表注释</label><input v-model="comment" :disabled="isAlter && !includeOptions" placeholder="可选" /></div>
            </div>
          </div>
        </div>

        <div v-if="error" class="alert alert-error"><Icon name="alert" :size="14" /><span class="grow">{{ error }}</span></div>

        <div v-if="preview" class="card">
          <div class="card-head"><Icon name="code" :size="13" /><h4>DDL 预览（只读）</h4></div>
          <div class="card-body"><pre class="code ddl-preview">{{ preview }}</pre></div>
        </div>
      </template>
    </div>

    <div class="toolbar page-foot">
      <span class="faint" style="font-size: 11px">先预览生成的脚本，确认后再执行；只读连接会被后端拒绝。</span>
      <span class="grow"></span>
      <button class="btn" :disabled="!hasChanges || busy" @click="discard"><Icon name="close" :size="13" /> 放弃修改</button>
      <button class="btn" :disabled="!canSubmit || busy" @click="doPreview"><Icon name="eye" :size="13" /> 预览 DDL</button>
      <button class="btn btn-primary" :disabled="!canSubmit || busy" @click="submit">
        <span v-if="busy" class="spinner" style="width: 12px; height: 12px"></span>
        <Icon v-else name="save" :size="13" /> {{ isAlter ? "应用结构变更" : "创建表" }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.designer { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; overflow: hidden; }
.page-head, .page-foot, .target-bar, .subtabs { flex-shrink: 0; }
.target-bar { display: flex; gap: 10px; padding: 8px 10px; background: var(--surface); border-bottom: 1px solid var(--border); }
.target-bar .field { display: flex; flex-direction: column; gap: 3px; min-width: 180px; }
.target-bar .field-label { font-size: 11px; color: var(--text-faint); }
.subtabs { display: flex; gap: 2px; padding: 0 10px; background: var(--surface); border-bottom: 1px solid var(--border); }
.subtabs button { display: inline-flex; align-items: center; gap: 5px; padding: 8px 14px; border: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--text-faint); font-size: 12px; cursor: pointer; }
.subtabs button:hover { color: var(--text); }
.subtabs button.active { color: var(--accent); border-bottom-color: var(--accent); }
.page-body { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 10px; padding: 10px; background: var(--bg); }
.page-body > .card, .subpane > .card { flex-shrink: 0; }
.subpane { display: flex; flex-direction: column; gap: 10px; }
.table-scroll { max-height: 320px; border: 1px solid var(--border); border-radius: var(--radius-sm); min-width: 0; }
.table-scroll input, .table-scroll select { padding: 3px 6px; font-size: 11px; }
.row-dropped td { opacity: .5; text-decoration: line-through; }
.index-list { list-style: none; margin: 0 0 8px; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.index-list li { display: flex; align-items: center; gap: 10px; font-size: 12px; }
.ddl-preview { white-space: pre-wrap; font-size: 11px; line-height: 1.6; margin: 0; max-height: 220px; overflow: auto; }
</style>
