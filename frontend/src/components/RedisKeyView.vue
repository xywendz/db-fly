<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { Cell, QueryOutcome } from "@/types/domain";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const db = props.tab.database ?? props.tab.ref?.database ?? "";
const initialKey = props.tab.ref?.name ?? "";
const mode = ref<"create" | "edit">(initialKey ? "edit" : "create");
// 已存在的键默认以只读（浏览）方式打开；点击「修改」进入编辑态。新建键始终可编辑。
const readOnly = ref<boolean>(initialKey !== "" && !!props.tab.readOnly);
const effectiveReadOnly = computed(() => mode.value === "create" ? false : readOnly.value);
function enterEdit() {
  readOnly.value = false;
}

const keyName = ref(initialKey);
const keyType = ref<string>("string"); // create 模式可选；edit 模式为探测到的类型
const ttl = ref<number>(-1);
const loading = ref(false);
const error = ref("");
const unsupported = ref("");

// 各类型的可编辑数据模型。
const data = reactive({
  stringValue: "",
  hashFields: [] as { field: string; value: string }[],
  listItems: [] as string[],
  setMembers: [] as string[],
  zsetMembers: [] as { member: string; score: string }[],
});

// 打开时的快照，用于判断是否有改动。
const original = ref<{ key: string; ttl: number; data: string }>({ key: "", ttl: -1, data: "" });

const effectiveType = computed(() => keyType.value);
const keyTypeLabel = computed(() =>
  mode.value === "create"
    ? `新建 · ${keyType.value}`
    : readOnly.value
      ? `浏览 · ${keyType.value}`
      : `编辑 · ${keyType.value}`,
);

const contentChanged = computed(() => JSON.stringify(data) !== original.value.data);
const ttlChanged = computed(() => ttl.value !== original.value.ttl);
const nameChanged = computed(() => keyName.value.trim() !== original.value.key);
const dirty = computed(() => contentChanged.value || ttlChanged.value || nameChanged.value);

const canSave = computed(() => {
  if (!keyName.value.trim()) return false;
  if (mode.value === "create" && effectiveType.value !== "string") {
    const has =
      (effectiveType.value === "hash" && data.hashFields.length > 0) ||
      (effectiveType.value === "list" && data.listItems.length > 0) ||
      (effectiveType.value === "set" && data.setMembers.length > 0) ||
      (effectiveType.value === "zset" && data.zsetMembers.length > 0);
    if (!has) return false;
  }
  return mode.value === "create" || dirty.value;
});

store.leftCollapsed = false;

async function redisQuery(text: string): Promise<QueryOutcome> {
  return api.executeQuery({
    conn: props.tab.conn,
    query: { language: "redis", text, database: db },
  });
}
async function redisExec(text: string): Promise<void> {
  await api.execStatement({ conn: props.tab.conn, statement: text, database: db });
}

function rowTexts(row: Cell[]): string[] {
  return row.map((c) => c?.text ?? "");
}
function valOf(row: Cell[]): string {
  const t = rowTexts(row);
  return t.length > 1 ? t[1] : t[0] ?? "";
}

// 把值用双引号包裹并转义内部引号/反斜杠，交给后端 splitCommand 解析。
function q(s: string): string {
  return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

async function load() {
  if (mode.value !== "edit") return;
  loading.value = true;
  error.value = "";
  unsupported.value = "";
  try {
    const tRes = await redisQuery(`TYPE ${q(keyName.value)}`);
    const typ = valOf(tRes.pages[0]?.rows?.[0] ?? []) || "none";
    keyType.value = typ;
    const ttlRes = await redisQuery(`TTL ${q(keyName.value)}`);
    ttl.value = Number(valOf(ttlRes.pages[0]?.rows?.[0] ?? [])) || -1;

    // 读取各类型的值
    if (typ === "string") {
      const r = await redisQuery(`GET ${q(keyName.value)}`);
      data.stringValue = valOf(r.pages[0]?.rows?.[0] ?? []);
    } else if (typ === "hash") {
      const r = await redisQuery(`HGETALL ${q(keyName.value)}`);
      const flat = (r.pages[0]?.rows ?? []).map(valOf);
      data.hashFields = [];
      for (let i = 0; i + 1 < flat.length; i += 2) {
        data.hashFields.push({ field: flat[i], value: flat[i + 1] });
      }
    } else if (typ === "list") {
      const r = await redisQuery(`LRANGE ${q(keyName.value)} 0 -1`);
      data.listItems = (r.pages[0]?.rows ?? []).map(valOf);
    } else if (typ === "set") {
      const r = await redisQuery(`SMEMBERS ${q(keyName.value)}`);
      data.setMembers = (r.pages[0]?.rows ?? []).map(valOf);
    } else if (typ === "zset") {
      const r = await redisQuery(`ZRANGE ${q(keyName.value)} 0 -1 WITHSCORES`);
      const flat = (r.pages[0]?.rows ?? []).map(valOf);
      data.zsetMembers = [];
      for (let i = 0; i + 1 < flat.length; i += 2) {
        data.zsetMembers.push({ member: flat[i], score: flat[i + 1] });
      }
    } else {
      unsupported.value = typ;
    }
    original.value = { key: keyName.value, ttl: ttl.value, data: JSON.stringify(data) };
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    loading.value = false;
  }
}

function buildContent(name: string, type: string): string[] {
  switch (type) {
    case "string":
      return [`SET ${q(name)} ${q(data.stringValue)}`];
    case "hash":
      if (!data.hashFields.length) return [];
      return [
        `HSET ${q(name)} ${data.hashFields.map((f) => `${q(f.field)} ${q(f.value)}`).join(" ")}`,
      ];
    case "list":
      if (!data.listItems.length) return [];
      return [`RPUSH ${q(name)} ${data.listItems.map(q).join(" ")}`];
    case "set":
      if (!data.setMembers.length) return [];
      return [`SADD ${q(name)} ${data.setMembers.map(q).join(" ")}`];
    case "zset":
      if (!data.zsetMembers.length) return [];
      return [
        `ZADD ${q(name)} ${data.zsetMembers.map((m) => `${q(m.score)} ${q(m.member)}`).join(" ")}`,
      ];
    default:
      return [];
  }
}

async function save() {
  error.value = "";
  const newName = keyName.value.trim();
  if (!newName) {
    error.value = "键名不能为空";
    return;
  }
  try {
    if (mode.value === "create") {
      const cmds = buildContent(newName, keyType.value);
      if (!cmds.length) {
        error.value = "该类型至少需要一个元素";
        return;
      }
      for (const c of cmds) await redisExec(c);
      if (ttl.value >= 0) await redisExec(`EXPIRE ${q(newName)} ${ttl.value}`);
      mode.value = "edit";
      store.setStatus(`已创建键 ${newName}`, "ok");
    } else {
      const cmds: string[] = [];
      const target = newName;
      if (nameChanged.value) cmds.push(`RENAME ${q(original.value.key)} ${q(newName)}`);
      if (contentChanged.value) {
        cmds.push(`DEL ${q(target)}`);
        cmds.push(...buildContent(target, keyType.value));
        if (ttl.value >= 0) cmds.push(`EXPIRE ${q(target)} ${ttl.value}`);
        else cmds.push(`PERSIST ${q(target)}`);
      } else if (ttlChanged.value) {
        if (ttl.value >= 0) cmds.push(`EXPIRE ${q(target)} ${ttl.value}`);
        else cmds.push(`PERSIST ${q(target)}`);
      }
      if (!cmds.length) {
        store.setStatus("没有改动", "info");
        return;
      }
      for (const c of cmds) await redisExec(c);
      store.setStatus(`已保存键 ${newName}`, "ok");
    }
    // 同步页签标题与引用，并刷新快照
    props.tab.title = keyName.value ? `键 · ${keyName.value}` : props.tab.title;
    if (props.tab.ref) props.tab.ref.name = newName;
    await load();
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`保存失败：${error.value}`, "error");
  }
}

async function delKey() {
  const ok = await store.confirm({
    title: "删除键",
    message: `确认删除键「${keyName.value}」？该操作不可恢复。`,
    danger: true,
    confirmText: "删除",
  });
  if (!ok) return;
  try {
    await redisExec(`DEL ${q(keyName.value)}`);
    store.setStatus(`已删除键 ${keyName.value}`, "ok");
    store.closeTab(props.tab.id);
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  }
}

function refresh() {
  load();
}

// 各类型的行内增删
function addHash() {
  data.hashFields.push({ field: "", value: "" });
}
function removeHash(i: number) {
  data.hashFields.splice(i, 1);
}
function addList() {
  data.listItems.push("");
}
function removeList(i: number) {
  data.listItems.splice(i, 1);
}
function addSet() {
  data.setMembers.push("");
}
function removeSet(i: number) {
  data.setMembers.splice(i, 1);
}
function addZ() {
  data.zsetMembers.push({ member: "", score: "0" });
}
function removeZ(i: number) {
  data.zsetMembers.splice(i, 1);
}

onMounted(load);
</script>

<template>
  <div class="rk-wrap">
    <div class="rk-bar card">
      <span class="badge" :class="mode === 'create' ? 'badge-ok' : readOnly ? 'badge-accent' : 'badge-warn'">{{ keyTypeLabel }}</span>
      <label class="rk-lbl">键名</label>
      <input v-model="keyName" class="rk-key" :disabled="loading || effectiveReadOnly" placeholder="键名" />
      <label class="rk-lbl">TTL(秒)</label>
      <input v-model.number="ttl" type="number" class="rk-ttl" :disabled="loading || effectiveReadOnly" title="-1 表示永久" />
      <span class="grow"></span>
      <button class="btn btn-icon" title="刷新" :disabled="loading" @click="refresh">
        <Icon name="refresh" :size="14" />
      </button>
      <button v-if="effectiveReadOnly" class="btn btn-primary btn-sm" @click="enterEdit">
        <Icon name="edit" :size="13" /> 修改
      </button>
      <button v-else class="btn btn-primary btn-sm" :disabled="!canSave || loading" @click="save">
        <Icon name="save" :size="13" /> 保存
      </button>
      <button v-if="mode !== 'create'" class="btn btn-sm btn-danger" :disabled="loading" @click="delKey">
        <Icon name="trash" :size="13" /> 删除键
      </button>
    </div>

    <div v-if="error" class="rk-error">{{ error }}</div>

    <div v-if="loading" class="rk-body scroll">
      <div class="loading-line" style="padding: 10px"><span class="spinner"></span> 正在读取键…</div>
    </div>

    <div v-else-if="unsupported" class="rk-body scroll">
      <div class="faint" style="padding: 16px">
        当前键类型为 <strong>{{ unsupported }}</strong>，本编辑器暂不支持可视化编辑。
        请使用查询页执行原生命令（如 <code>GET</code> / <code>LRANGE</code> 等）查看与修改。
      </div>
    </div>

    <div v-else class="rk-body scroll">
      <div v-if="mode === 'create'" class="rk-create-type">
        <label class="rk-lbl">类型</label>
        <select v-model="keyType" class="rk-ttl">
          <option value="string">string</option>
          <option value="hash">hash</option>
          <option value="list">list</option>
          <option value="set">set</option>
          <option value="zset">zset</option>
        </select>
        <span class="faint" style="font-size: 11px">新建键的初始内容与类型。</span>
      </div>

      <!-- string -->
      <template v-if="effectiveType === 'string'">
        <label class="rk-field">值</label>
        <textarea v-model="data.stringValue" class="rk-textarea" rows="12" :disabled="loading || effectiveReadOnly" placeholder="字符串内容"></textarea>
      </template>

      <!-- hash -->
      <template v-else-if="effectiveType === 'hash'">
        <div class="rk-row-head">
          <span>字段</span><span>值</span><span></span>
        </div>
        <div v-for="(f, i) in data.hashFields" :key="i" class="rk-kv">
          <input v-model="f.field" placeholder="field" :disabled="loading || effectiveReadOnly" />
          <input v-model="f.value" placeholder="value" :disabled="loading || effectiveReadOnly" />
          <button v-if="!effectiveReadOnly" class="btn btn-icon" title="删除" @click="removeHash(i)"><Icon name="close" :size="12" /></button>
        </div>
        <button v-if="!effectiveReadOnly" class="btn btn-sm" @click="addHash"><Icon name="plus" :size="12" /> 添加字段</button>
      </template>

      <!-- list -->
      <template v-else-if="effectiveType === 'list'">
        <div class="rk-row-head"><span>索引</span><span>值</span><span></span></div>
        <div v-for="(v, i) in data.listItems" :key="i" class="rk-kv">
          <span class="dim mono rk-idx">{{ i }}</span>
          <input v-model="data.listItems[i]" placeholder="value" :disabled="loading || effectiveReadOnly" />
          <button v-if="!effectiveReadOnly" class="btn btn-icon" title="删除" @click="removeList(i)"><Icon name="close" :size="12" /></button>
        </div>
        <button v-if="!effectiveReadOnly" class="btn btn-sm" @click="addList"><Icon name="plus" :size="12" /> 添加元素</button>
      </template>

      <!-- set -->
      <template v-else-if="effectiveType === 'set'">
        <div class="rk-row-head"><span>成员</span><span></span><span></span></div>
        <div v-for="(m, i) in data.setMembers" :key="i" class="rk-kv">
          <input v-model="data.setMembers[i]" placeholder="member" :disabled="loading || effectiveReadOnly" />
          <span></span>
          <button v-if="!effectiveReadOnly" class="btn btn-icon" title="删除" @click="removeSet(i)"><Icon name="close" :size="12" /></button>
        </div>
        <button v-if="!effectiveReadOnly" class="btn btn-sm" @click="addSet"><Icon name="plus" :size="12" /> 添加成员</button>
      </template>

      <!-- zset -->
      <template v-else-if="effectiveType === 'zset'">
        <div class="rk-row-head"><span>成员</span><span>分数</span><span></span></div>
        <div v-for="(m, i) in data.zsetMembers" :key="i" class="rk-kv">
          <input v-model="data.zsetMembers[i].member" placeholder="member" :disabled="loading || effectiveReadOnly" />
          <input v-model="data.zsetMembers[i].score" placeholder="score" class="rk-score" :disabled="loading || effectiveReadOnly" />
          <button v-if="!effectiveReadOnly" class="btn btn-icon" title="删除" @click="removeZ(i)"><Icon name="close" :size="12" /></button>
        </div>
        <button v-if="!effectiveReadOnly" class="btn btn-sm" @click="addZ"><Icon name="plus" :size="12" /> 添加成员</button>
      </template>

      <div v-if="dirty && !effectiveReadOnly" class="faint" style="padding: 6px 10px; font-size: 11px">
        有未保存的改动
      </div>
    </div>
  </div>
</template>

<style scoped>
.rk-wrap { display: flex; flex-direction: column; height: 100%; background: var(--surface); }
.rk-bar { display: flex; align-items: center; gap: 8px; margin: 8px; padding: 8px; flex-wrap: wrap; }
.rk-lbl { font-size: 12px; color: var(--text-dim); }
.rk-key { width: 240px; }
.rk-ttl { width: 90px; }
.rk-create-type { display: flex; align-items: center; gap: 8px; padding: 0 10px 8px; }
.rk-error { margin: 0 8px 8px; padding: 8px 10px; border-radius: 6px; background: color-mix(in srgb, var(--danger) 14%, transparent); color: var(--danger); font-size: 12px; }
.rk-body { flex: 1; min-height: 0; padding: 4px 10px 12px; }
.rk-field { display: block; font-size: 12px; color: var(--text-dim); margin: 8px 0 4px; }
.rk-textarea { width: 100%; font-family: var(--mono); font-size: 13px; }
.rk-row-head { display: grid; grid-template-columns: 1fr 1fr 28px; gap: 6px; font-size: 11px; color: var(--text-dim); padding: 8px 2px 4px; }
.rk-kv { display: grid; grid-template-columns: 1fr 1fr 28px; gap: 6px; margin-bottom: 4px; }
.rk-kv input { width: 100%; }
.rk-idx { align-self: center; text-align: right; padding-right: 6px; }
.rk-score { max-width: 120px; }
</style>
