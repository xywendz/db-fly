<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ColumnInfo } from "@/types/domain";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const db = props.tab.database ?? props.tab.ref?.database ?? "";
const initialName = props.tab.ref?.name ?? "";
const mode = ref<"create" | "edit">(initialName ? "edit" : "create");

const collName = ref(initialName);
const validatorText = ref(""); // 集合校验器（$jsonSchema 等）的 JSON 文本，留空表示不限制
const indexes = ref<{ name: string; keys: string; unique: boolean }[]>([]);
const sampledFields = ref<ColumnInfo[]>([]); // 采样推断的字段，只读展示
const capped = ref(false);
const cappedSize = ref<number>(0);
const cappedMax = ref<number>(0);

const loading = ref(false);
const error = ref("");

// 打开时的快照，用于判断是否有改动。
const original = ref<{ validator: string; indexes: string }>({ validator: "", indexes: "" });

const isCreate = computed(() => mode.value === "create");
const badgeLabel = computed(() => (isCreate.value ? "新建集合" : "集合结构"));

const dirty = computed(
  () =>
    validatorText.value !== original.value.validator ||
    JSON.stringify(indexes.value) !== original.value.indexes,
);

const canSave = computed(() => {
  if (!collName.value.trim()) return false;
  if (mode.value === "create") return true;
  return dirty.value;
});

store.leftCollapsed = false;

async function mongoExec(statement: string): Promise<void> {
  await api.execStatement({ conn: props.tab.conn, statement, database: db });
}

// 校验器文本 → JSON 对象；空文本返回 undefined（不限制结构）。
function parseValidator(text: string): any {
  const t = text.trim();
  if (!t) return undefined;
  const v = JSON.parse(t);
  if (v === null || typeof v !== "object" || Array.isArray(v)) {
    throw new Error("校验器必须是 JSON 对象");
  }
  return v;
}

// "field1:1,field2:-1" → { field1: 1, field2: -1 }
function parseKeys(text: string): Record<string, number> {
  const out: Record<string, number> = {};
  for (const part of text.split(",")) {
    const t = part.trim();
    if (!t) continue;
    const idx = t.lastIndexOf(":");
    let field = t;
    let dir: number = 1;
    if (idx > 0) {
      field = t.slice(0, idx).trim();
      dir = t.slice(idx + 1).trim() === "-1" ? -1 : 1;
    }
    if (field) out[field] = dir;
  }
  return out;
}

async function load() {
  if (mode.value !== "edit") return;
  loading.value = true;
  error.value = "";
  try {
    const schema = await api.describeObject(props.tab.conn, {
      database: db,
      name: collName.value,
    });
    validatorText.value = schema.comment ?? "";
    indexes.value = (schema.indexes ?? [])
      .filter((i) => i.name !== "_id_")
      .map((i) => ({ name: i.name, keys: (i.columns ?? []).map((c) => `${c}:1`).join(","), unique: !!i.unique }));
    sampledFields.value = schema.columns ?? [];
    capped.value = schema.engine === "capped";
    original.value = {
      validator: validatorText.value,
      indexes: JSON.stringify(indexes.value),
    };
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    loading.value = false;
  }
}

async function save() {
  error.value = "";
  const name = collName.value.trim();
  if (!name) {
    error.value = "集合名不能为空";
    return;
  }
  try {
    if (mode.value === "create") {
      const cmd: any = { create: name };
      const v = parseValidator(validatorText.value);
      if (v !== undefined) cmd.validator = v;
      if (capped.value) {
        cmd.capped = true;
        if (cappedSize.value > 0) cmd.size = cappedSize.value;
        if (cappedMax.value > 0) cmd.max = cappedMax.value;
      }
      await mongoExec(JSON.stringify(cmd));
      for (const idx of indexes.value) {
        if (!idx.name.trim()) continue;
        await mongoExec(
          JSON.stringify({
            createIndexes: name,
            indexes: [{ key: parseKeys(idx.keys), name: idx.name.trim(), unique: !!idx.unique }],
          }),
        );
      }
      mode.value = "edit";
      props.tab.title = `集合 · ${name}`;
      if (props.tab.ref) props.tab.ref.name = name;
      await load();
      store.setStatus(`已创建集合 ${name}`, "ok");
    } else {
      const cmds: string[] = [];
      const v = parseValidator(validatorText.value);
      if (validatorText.value.trim() !== original.value.validator) {
        if (v === undefined) {
          error.value = "编辑模式下校验器不能为空（如需移除校验请使用查询页执行 collMod）";
          return;
        }
        cmds.push(JSON.stringify({ collMod: name, validator: v, validationLevel: "moderate" }));
      }
      const origNames = new Set<string>(
        (JSON.parse(original.value.indexes) as { name: string }[]).map((x) => x.name),
      );
      const curNames = new Set(indexes.value.map((x) => x.name));
      for (const idx of indexes.value) {
        if (!idx.name.trim()) continue;
        if (!origNames.has(idx.name)) {
          cmds.push(
            JSON.stringify({
              createIndexes: name,
              indexes: [{ key: parseKeys(idx.keys), name: idx.name.trim(), unique: !!idx.unique }],
            }),
          );
        }
      }
      for (const n of origNames) {
        if (!curNames.has(n)) {
          cmds.push(JSON.stringify({ dropIndexes: name, index: n }));
        }
      }
      if (!cmds.length) {
        store.setStatus("没有改动", "info");
        return;
      }
      for (const c of cmds) await mongoExec(c);
      await load();
      store.setStatus(`已保存集合 ${name}`, "ok");
    }
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`保存失败：${error.value}`, "error");
  }
}

async function dropColl() {
  const ok = await store.confirm({
    title: "删除集合",
    message: `确认删除集合「${collName.value}」？该操作不可恢复。`,
    danger: true,
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await mongoExec(JSON.stringify({ drop: collName.value.trim() }));
    store.setStatus(`已删除集合 ${collName.value}`, "ok");
    store.closeTab(props.tab.id);
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  }
}

function refresh() {
  load();
}
function addIndex() {
  indexes.value.push({ name: "", keys: "", unique: false });
}
function removeIndex(i: number) {
  indexes.value.splice(i, 1);
}

onMounted(load);
</script>

<template>
  <div class="mc-wrap">
    <div class="mc-bar card">
      <span class="badge" :class="isCreate ? 'badge-ok' : 'badge-accent'">{{ badgeLabel }}</span>
      <label class="mc-lbl">集合名</label>
      <input v-model="collName" class="mc-name" :disabled="loading || !isCreate" :placeholder="isCreate ? '集合名' : ''" />
      <span class="grow"></span>
      <button class="btn btn-icon" title="刷新" :disabled="!isCreate && loading" @click="refresh">
        <Icon name="refresh" :size="14" />
      </button>
      <button class="btn btn-primary btn-sm" :disabled="!canSave || loading" @click="save">
        <Icon name="save" :size="13" /> 保存
      </button>
      <button v-if="!isCreate" class="btn btn-sm btn-danger" :disabled="loading" @click="dropColl">
        <Icon name="trash" :size="13" /> 删除集合
      </button>
    </div>

    <div v-if="error" class="mc-error">{{ error }}</div>

    <div v-if="loading" class="mc-body scroll">
      <div class="loading-line" style="padding: 10px"><span class="spinner"></span> 正在读取集合…</div>
    </div>

    <div v-else class="mc-body scroll">
      <div v-if="!isCreate && sampledFields.length" class="mc-section">
        <div class="mc-sec-title">采样字段（推断，只读）</div>
        <table class="data-table compact">
          <thead>
            <tr><th>字段路径</th><th>推断类型</th><th>说明</th></tr>
          </thead>
          <tbody>
            <tr v-for="f in sampledFields" :key="f.name">
              <td class="mono">{{ f.name }}</td>
              <td class="mono dim">{{ f.data_type }}</td>
              <td class="dim">{{ f.comment || "—" }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="mc-section">
        <div class="mc-sec-title">索引</div>
        <div class="mc-row-head"><span>名称</span><span>键 (field:1,field:-1)</span><span>唯一</span><span></span></div>
        <div v-for="(idx, i) in indexes" :key="i" class="mc-idx">
          <input v-model="idx.name" placeholder="idx_name" />
          <input v-model="idx.keys" placeholder="field1:1,field2:-1" />
          <label class="mc-chk"><input type="checkbox" v-model="idx.unique" /> 唯一</label>
          <button class="btn btn-icon" title="删除" @click="removeIndex(i)"><Icon name="close" :size="12" /></button>
        </div>
        <button class="btn btn-sm" @click="addIndex"><Icon name="plus" :size="12" /> 添加索引</button>
      </div>

      <div class="mc-section">
        <div class="mc-sec-title">校验器 (JSON Schema)</div>
        <textarea
          v-model="validatorText"
          class="mc-textarea"
          rows="12"
          placeholder='留空表示不限制结构，例如：&#10;{ "$jsonSchema": { "bsonType": "object", "required": ["name"], "properties": { "name": { "bsonType": "string" } } } }'
        ></textarea>
        <div class="faint" style="font-size: 11px; margin-top: 4px">
          留空 = 不限制；填写后保存会整体替换集合的 validator（collMod）。
        </div>
      </div>

      <div v-if="isCreate" class="mc-section">
        <div class="mc-sec-title">选项</div>
        <label class="mc-chk"><input type="checkbox" v-model="capped" /> 定长集合 (capped)</label>
        <div v-if="capped" class="mc-opts">
          <label class="mc-lbl">大小(字节)</label>
          <input v-model.number="cappedSize" type="number" class="mc-num" min="0" />
          <label class="mc-lbl">最大文档数</label>
          <input v-model.number="cappedMax" type="number" class="mc-num" min="0" />
        </div>
      </div>

      <div v-if="dirty" class="faint" style="padding: 6px 2px; font-size: 11px">有未保存的改动</div>
    </div>
  </div>
</template>

<style scoped>
.mc-wrap { display: flex; flex-direction: column; height: 100%; background: var(--surface); }
.mc-bar { display: flex; align-items: center; gap: 8px; margin: 8px; padding: 8px; flex-wrap: wrap; }
.mc-lbl { font-size: 12px; color: var(--text-dim); }
.mc-name { width: 240px; }
.mc-body { flex: 1; min-height: 0; padding: 4px 12px 16px; }
.mc-error { margin: 0 8px 8px; padding: 8px 10px; border-radius: 6px; background: color-mix(in srgb, var(--danger) 14%, transparent); color: var(--danger); font-size: 12px; }
.mc-section { margin-top: 14px; }
.mc-sec-title { font-size: 12px; color: var(--text-dim); margin-bottom: 6px; font-weight: 600; }
.mc-row-head { display: grid; grid-template-columns: 1.2fr 2fr 0.8fr 28px; gap: 6px; font-size: 11px; color: var(--text-dim); padding: 4px 2px; }
.mc-idx { display: grid; grid-template-columns: 1.2fr 2fr 0.8fr 28px; gap: 6px; margin-bottom: 4px; }
.mc-idx input { width: 100%; }
.mc-chk { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-dim); }
.mc-textarea { width: 100%; font-family: var(--mono); font-size: 13px; }
.mc-opts { display: flex; align-items: center; gap: 8px; margin-top: 6px; flex-wrap: wrap; }
.mc-num { width: 100px; }
.data-table.compact { font-size: 12px; }
.data-table.compact th, .data-table.compact td { padding: 3px 8px; }
</style>
