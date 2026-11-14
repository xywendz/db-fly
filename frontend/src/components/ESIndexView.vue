<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ColumnSpec, Script, SchemaChange } from "@/types/domain";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const db = props.tab.database ?? props.tab.ref?.database ?? "";
const initialName = props.tab.ref?.name ?? "";
const isCreate = !initialName && props.tab.mode !== "modify";
const isModify = !!initialName && props.tab.mode === "modify";
// 在「索引定义」查看页内直接切入修改（无需单独的右键项 / 独立 tab）。
const editing = ref(false);
const isEditing = computed(() => isModify || editing.value);

const name = ref(initialName);
const shards = ref(1);
const replicas = ref(1);
const refreshInterval = ref("1s");
const aliasesText = ref("");
const advancedSettings = ref("");
const advancedMappings = ref("");
const fields = ref<{ name: string; type: string }[]>([{ name: "", type: "text" }]);

const busy = ref(false);
const error = ref("");
const script = ref<Script | null>(null);
const definition = ref("");
const applied = ref(false);

const ES_TYPES = [
  "text", "keyword", "long", "integer", "double", "boolean",
  "date", "object", "nested", "geo_point", "ip", "binary",
];

// 逻辑库 + 相对索引名 → 物理索引名，与后端 physicalIndex 规则保持一致。
const physical = computed(() => {
  const n = name.value.trim();
  if (!n || !db || db === "default") return n;
  return n.startsWith(`${db}.`) ? n : `${db}.${n}`;
});

store.leftCollapsed = false;

function parseJSONObject(text: string, label: string): Record<string, any> | null {
  const t = text.trim();
  if (!t) return null;
  const value = JSON.parse(t);
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} 必须是 JSON 对象`);
  }
  return value as Record<string, any>;
}

function buildOptions(): Record<string, any> {
  const options: Record<string, any> = {
    number_of_shards: Number(shards.value) || 1,
    number_of_replicas: Number(replicas.value) || 0,
  };
  if (refreshInterval.value.trim()) options.refresh_interval = refreshInterval.value.trim();
  const settings = parseJSONObject(advancedSettings.value, "高级 settings");
  if (settings) options.settings = settings;
  const mappings = parseJSONObject(advancedMappings.value, "高级 mappings");
  if (mappings) options.mappings = mappings;
  const aliases = aliasesText.value.split(",").map((s) => s.trim()).filter(Boolean);
  if (aliases.length) {
    options.aliases = Object.fromEntries(aliases.map((a) => [a, {}]));
  }
  return options;
}

function buildDefinition() {
  const columns: ColumnSpec[] = fields.value
    .filter((f) => f.name.trim())
    .map((f) => ({ name: f.name.trim(), type: f.type, nullable: true, primary_key: false }));
  return {
    ref: { database: db, name: name.value.trim() },
    columns,
    options: JSON.stringify(buildOptions()),
    if_not_exists: false,
  };
}

// 修改模式：把「新增字段 / 高级 mappings / 高级 settings」组装为结构化 SchemaChange 列表。
// 后端 AlterTableScript 据此生成 PUT /_mapping（新增字段）与 PUT /_settings（更新设置）。
function buildChanges(): SchemaChange[] {
  const changes: SchemaChange[] = [];
  for (const f of fields.value) {
    if (f.name.trim()) {
      changes.push({
        kind: "add_column",
        column: { name: f.name.trim(), type: f.type, nullable: true, primary_key: false },
      });
    }
  }
  const mappings = parseJSONObject(advancedMappings.value, "高级 mappings");
  if (mappings) {
    changes.push({ kind: "add_column", options: JSON.stringify(mappings) });
  }
  const settings = parseJSONObject(advancedSettings.value, "高级 settings");
  if (settings) {
    changes.push({ kind: "collection_options", options: JSON.stringify(settings) });
  }
  return changes;
}

async function preview() {
  error.value = "";
  script.value = null;
  busy.value = true;
  try {
    script.value = await api.previewCreateTable(props.tab.conn, buildDefinition());
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
}

async function apply() {
  if (!script.value) return;
  const ok = await store.confirm({
    title: "创建 Elasticsearch 索引",
    message: `将在连接「${props.tab.conn}」上创建索引 ${physical.value}。请先核对下方 Elasticsearch 请求。`,
    confirmText: "执行脚本",
  });
  if (!ok) return;
  busy.value = true;
  error.value = "";
  try {
    await api.applyScript(props.tab.conn, script.value);
    applied.value = true;
    script.value = null;
    store.invalidateObjects(props.tab.conn, db);
    delete (store.dbCache as Record<string, any>)[props.tab.conn];
    store.notify(`索引 ${physical.value} 创建成功`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
}

async function previewModify() {
  error.value = "";
  script.value = null;
  busy.value = true;
  try {
    const changes = buildChanges();
    if (!changes.length) {
      error.value = "请至少新增一个字段，或在「高级 mappings / settings」中填写一项。";
      return;
    }
    script.value = await api.previewAlterTable(
      props.tab.conn,
      { database: db, name: initialName },
      changes,
    );
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
}

async function applyModify() {
  if (!script.value) return;
  const ok = await store.confirm({
    title: "修改 Elasticsearch 索引 Mapping",
    message:
      `将在连接「${props.tab.conn}」上修改索引 ${physical.value}。` +
      "Elasticsearch 只允许新增字段 / 更新 settings，已有字段类型不可修改。请先核对下方请求。",
    confirmText: "执行脚本",
  });
  if (!ok) return;
  busy.value = true;
  error.value = "";
  try {
    await api.applyScript(props.tab.conn, script.value);
    applied.value = true;
    script.value = null;
    store.invalidateObjects(props.tab.conn, db);
    delete (store.dbCache as Record<string, any>)[props.tab.conn];
    store.notify(`索引 ${physical.value} 的 mapping 已更新`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
}

async function copyScript() {
  const text = script.value?.statements.join("\n") ?? "";
  if (!text) {
    store.setStatus("暂无可复制的脚本", "warn");
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    store.notify("脚本已复制到剪贴板", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

async function copyDefinition() {
  if (!definition.value) {
    store.setStatus("暂无可复制的脚本", "warn");
    return;
  }
  try {
    await navigator.clipboard.writeText(definition.value);
    store.notify("脚本已复制到剪贴板", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

function onPreview() {
  return isEditing.value ? previewModify() : preview();
}
function onApply() {
  return isEditing.value ? applyModify() : apply();
}
function cancelEdit() {
  editing.value = false;
  script.value = null;
  error.value = "";
}

onMounted(async () => {
  if (isCreate) return;
  busy.value = true;
  try {
    definition.value = await api.showCreateObject(props.tab.conn, { database: db, name: initialName });
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    busy.value = false;
  }
});
</script>

<template>
  <div class="page">
    <div class="toolbar page-head">
      <Icon name="search" :size="15" />
      <strong>{{ isCreate ? "新建索引" : isEditing ? "修改 Mapping" : "索引定义" }}</strong>
      <span class="badge">{{ props.tab.conn }}</span>
      <span v-if="db" class="badge">{{ db }}</span>
      <span class="grow"></span>
      <span v-if="physical" class="badge badge-ok">{{ physical }}</span>
    </div>

    <div class="page-body scroll">
      <div v-if="error" class="alert alert-error">
        <Icon name="alert" :size="14" /> <span>{{ error }}</span>
      </div>
      <div v-if="applied" class="alert alert-ok">
        <Icon name="check" :size="14" />
        <span>{{ isCreate ? "索引已创建，可在左侧资源树刷新后浏览或导入数据。" : "索引 mapping 已更新，可在左侧资源树刷新后浏览。" }}</span>
      </div>

      <!-- 修改模式：展示当前 mapping 作为参考（只读） -->
      <div v-if="isEditing" class="card">
        <div class="card-head">
          <Icon name="code" :size="14" /><h4>当前索引定义（只读参考）</h4>
          <span class="grow"></span>
          <button class="btn btn-sm" :disabled="!definition" @click="copyDefinition">
            <Icon name="copy" :size="12" /> 复制
          </button>
        </div>
        <div class="card-body">
          <pre class="mono script-preview">{{ definition || "正在读取…" }}</pre>
          <div class="alert alert-info">
            <Icon name="info" :size="14" />
            <span>Elasticsearch 只允许通过下方表单新增字段（PUT /_mapping）或更新 settings（PUT /_settings），已有字段类型不可修改。</span>
          </div>
        </div>
      </div>

      <!-- 新建 / 修改 共用：索引配置与映射编辑 -->
      <template v-if="isCreate || isEditing">
        <div v-if="isCreate" class="card">
          <div class="card-head"><Icon name="settings" :size="14" /><h4>1 · 索引配置</h4></div>
          <div class="card-body form-grid three">
            <div class="field">
              <label class="field-label">索引名 <span class="req">*</span></label>
              <input v-model="name" class="mono" placeholder="如 users" />
              <span class="field-hint">只能包含小写字母、数字与 . _ - +；物理名：{{ physical || "—" }}</span>
            </div>
            <div class="field">
              <label class="field-label">主分片数</label>
              <input v-model.number="shards" type="number" min="1" max="100" />
              <span class="field-hint">创建后不可修改，需要 reindex 才能调整。</span>
            </div>
            <div class="field">
              <label class="field-label">副本数</label>
              <input v-model.number="replicas" type="number" min="0" max="20" />
            </div>
            <div class="field">
              <label class="field-label">refresh_interval</label>
              <input v-model="refreshInterval" placeholder="1s；大批量导入可用 -1" />
            </div>
            <div class="field">
              <label class="field-label">别名（逗号分隔）</label>
              <input v-model="aliasesText" placeholder="如 users_current, users_read" />
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-head">
            <Icon name="table" :size="14" /><h4>{{ isCreate ? "2 · 字段 mapping" : "1 · 新增字段 mapping" }}</h4>
            <span class="grow"></span>
            <button class="btn btn-sm" @click="fields.push({ name: '', type: 'text' })">
              <Icon name="plus" :size="12" /> 添加字段
            </button>
          </div>
          <div class="card-body">
            <div v-for="(field, i) in fields" :key="i" class="row" style="gap: 8px; margin-bottom: 8px">
              <input v-model="field.name" class="mono" placeholder="字段名（_id 无需填写）" style="flex: 1" />
              <select v-model="field.type" style="width: 150px">
                <option v-for="t in ES_TYPES" :key="t" :value="t">{{ t }}</option>
              </select>
              <button class="btn btn-sm" :disabled="fields.length === 1" @click="fields.splice(i, 1)">
                <Icon name="trash" :size="12" />
              </button>
            </div>
            <span class="field-hint">
              新增字段会转换为 mappings.properties（text 自动带 .keyword 子字段）；含点路径的嵌套字段由动态映射展开。
            </span>
          </div>
        </div>

        <div class="card">
          <div class="card-head"><Icon name="code" :size="14" /><h4>{{ isCreate ? "3 · 高级选项（可选）" : "2 · 高级选项（可选）" }}</h4></div>
          <div class="card-body form-grid">
            <div class="field">
              <label class="field-label">{{ isCreate ? "完整 settings JSON（覆盖上方配置）" : "高级 mappings JSON（PUT /_mapping 的请求体）" }}</label>
              <textarea v-model="advancedMappings" class="mono" rows="5" :placeholder='isCreate ? "{\"analysis\":{...}}" : "{\"properties\":{\"title\":{\"type\":\"text\"}}} 或 {\"title\":{\"type\":\"text\"}}"'></textarea>
            </div>
            <div class="field">
              <label class="field-label">{{ isCreate ? "完整 mappings JSON（覆盖字段列表；字段可直接写，无需包 properties）" : "高级 settings JSON（PUT /_settings 的请求体）" }}</label>
              <textarea v-model="advancedSettings" class="mono" rows="5" :placeholder='isCreate ? "{\"title\":{\"type\":\"text\"}} 或 {\"properties\":{\"title\":{\"type\":\"text\"}}}" : "{\"number_of_replicas\":2,\"refresh_interval\":\"30s\"}"'></textarea>
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-head">
            <Icon name="eye" :size="14" /><h4>{{ isCreate ? "4 · 脚本预览与执行" : "3 · 脚本预览与执行" }}</h4>
            <span class="grow"></span>
            <button v-if="editing && !isModify" class="btn btn-sm" @click="cancelEdit">
              <Icon name="close" :size="12" /> 取消
            </button>
            <button class="btn btn-sm" :disabled="busy || (isCreate && !name.trim())" @click="onPreview">
              <span v-if="busy" class="spinner" style="width: 12px; height: 12px"></span>
              <Icon v-else name="refresh" :size="12" /> 生成脚本
            </button>
            <button class="btn btn-sm btn-primary" :disabled="busy || !script" @click="onApply">
              <Icon name="check" :size="12" /> 确认执行
            </button>
            <button class="btn btn-sm" :disabled="!script" @click="copyScript">
              <Icon name="copy" :size="12" /> 复制
            </button>
          </div>
          <div class="card-body col" style="gap: 8px">
            <div v-if="!script" class="field-hint">
              {{ isCreate
                ? "先生成 Elasticsearch 请求（PUT /索引 + settings / mappings）并核对，再确认执行。执行前会二次确认。"
                : "先生成 Elasticsearch 请求（PUT /_mapping 新增字段 / PUT /_settings 更新设置）并核对，再确认执行。" }}
            </div>
            <template v-else>
              <div v-if="script.warnings?.length" class="alert alert-warn">
                <Icon name="alert" :size="14" />
                <span>{{ script.warnings.join("；") }}</span>
              </div>
              <pre class="mono script-preview">{{ script.statements.join("\n") }}</pre>
              <span v-if="script.rollback?.length" class="field-hint">
                回滚（best-effort）：{{ script.rollback.join(" ") }}
              </span>
            </template>
          </div>
        </div>
      </template>

      <!-- 仅查看（非修改）：索引定义 PUT 脚本 -->
      <template v-else>
        <div class="card">
          <div class="card-head"><Icon name="code" :size="14" /><h4>索引定义（可回放的 PUT 脚本）</h4>
            <span class="grow"></span>
            <button class="btn btn-sm" :disabled="!definition" @click="editing = true">
              <Icon name="edit" :size="12" /> 修改
            </button>
            <button class="btn btn-sm" :disabled="!definition" @click="copyDefinition">
              <Icon name="copy" :size="12" /> 复制
            </button>
          </div>
          <div class="card-body">
            <pre class="mono script-preview">{{ definition || "正在读取…" }}</pre>
            <div class="alert alert-info">
              <Icon name="info" :size="14" />
              <span>新增字段请用「结构」页或执行 <code>PUT /&lt;索引&gt;/_mapping</code>；ES 不允许修改已有字段类型。</span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.page { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; overflow: hidden; }
.page-head { flex-shrink: 0; }
.page-body { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 10px; padding: 10px; background: var(--bg); }
.page-body > .card, .page-body > .alert { flex-shrink: 0; }
.script-preview {
  margin: 0;
  padding: 12px;
  max-height: 340px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--code-bg, rgba(127, 127, 127, 0.08));
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.6;
}
</style>
