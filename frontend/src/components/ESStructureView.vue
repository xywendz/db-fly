<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ObjectRef, Script } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();
const refObj = computed<ObjectRef>(() => props.tab.ref!);
const conn = computed(() => props.tab.conn);
const db = computed(() => refObj.value.database);

const loading = ref(false);
const error = ref("");
const definition = ref(""); // 原始 PUT /index 定义文本
const indexDef = ref<Record<string, any> | null>(null);
const activeTab = ref<"fields" | "settings" | "aliases" | "ddl">("fields");
const busy = ref(false);

interface FieldRow {
  path: string;
  type: string;
  subfields: string[];
  depth: number;
}

/** 从 mappings 中取出 properties：兼容 ES7+（顶层 properties）与 ES5/6（mappings.<type>.properties 包裹）。 */
function mappingProperties(mappings: any): Record<string, any> {
  if (!mappings || typeof mappings !== "object") return {};
  if (mappings.properties && typeof mappings.properties === "object") return mappings.properties;
  for (const key of Object.keys(mappings)) {
    const v = mappings[key];
    if (v && typeof v === "object" && v.properties && typeof v.properties === "object") return v.properties;
  }
  return {};
}

function flattenFields(props: Record<string, any>, prefix: string, depth: number, out: FieldRow[]) {
  for (const [name, raw] of Object.entries(props || {})) {
    const path = prefix ? `${prefix}.${name}` : name;
    const d = raw as any;
    const type = d && typeof d === "object" ? (d.type ?? "object") : String(raw);
    const subfields = d && d.fields && typeof d.fields === "object" ? Object.keys(d.fields) : [];
    out.push({ path, type, subfields, depth });
    if (d && d.properties && typeof d.properties === "object") {
      flattenFields(d.properties, path, depth + 1, out);
    }
  }
}

const fieldRows = computed<FieldRow[]>(() => {
  const out: FieldRow[] = [];
  if (indexDef.value) flattenFields(mappingProperties(indexDef.value["mappings"]), "", 0, out);
  return out;
});

const settingsRows = computed<{ key: string; value: string }[]>(() => {
  const out: { key: string; value: string }[] = [];
  function walk(obj: any, prefix: string) {
    if (!obj || typeof obj !== "object") return;
    for (const [k, v] of Object.entries(obj)) {
      const key = prefix ? `${prefix}.${k}` : k;
      if (v && typeof v === "object" && !Array.isArray(v)) walk(v, key);
      else out.push({ key, value: typeof v === "string" ? v : JSON.stringify(v) });
    }
  }
  walk(indexDef.value?.["settings"], "");
  return out;
});

const aliasRows = computed<{ name: string; config: string }[]>(() => {
  const a = indexDef.value?.["aliases"];
  if (!a || typeof a !== "object") return [];
  return Object.entries(a as Record<string, any>).map(([name, cfg]) => ({
    name,
    config: Object.keys(cfg || {}).length ? JSON.stringify(cfg) : "—",
  }));
});

async function load() {
  loading.value = true;
  error.value = "";
  try {
    const def = await api.showCreateObject(conn.value, refObj.value);
    definition.value = def;
    const nl = def.indexOf("\n");
    const jsonStr = nl >= 0 ? def.slice(nl + 1) : def;
    try {
      indexDef.value = JSON.parse(jsonStr);
    } catch {
      indexDef.value = null;
    }
    store.setStatus(`${refObj.value.name} 结构已加载`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`结构加载失败：${error.value}`, "error");
  } finally {
    loading.value = false;
  }
}
onMounted(load);

async function copyDefinition() {
  if (!definition.value) {
    store.setStatus("暂无可复制的定义", "warn");
    return;
  }
  try {
    await navigator.clipboard.writeText(definition.value);
    store.notify("索引结构已复制到剪贴板", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

async function copyMapping() {
  const m = indexDef.value?.["mappings"];
  if (!m) {
    store.setStatus("暂无可复制的 mapping", "warn");
    return;
  }
  try {
    await navigator.clipboard.writeText(JSON.stringify(m, null, 2));
    store.notify("mapping 已复制到剪贴板", "ok", 1800);
  } catch {
    store.setStatus("剪贴板不可用", "warn");
  }
}

async function dropIndex() {
  try {
    const script: Script = await api.previewDropObject(conn.value, refObj.value);
    const ok = await store.confirm({
      title: "删除索引",
      message: `将永久删除索引 ${refObj.value.name}（全部文档、mapping 与 settings），不可恢复。`,
      confirmText: "删除索引",
      danger: true,
      subject: refObj.value.name,
    });
    if (!ok) return;
    busy.value = true;
    await api.applyScript(conn.value, script);
    store.setStatus(`索引 ${refObj.value.name} 已删除`, "ok");
    store.invalidateObjects(conn.value, db.value);
    store.closeTab(props.tab.id);
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`删除失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="sv">
    <div class="toolbar">
      <Icon name="search" :size="15" />
      <strong>{{ refObj.name }}</strong>
      <span class="badge">{{ conn }}</span>
      <span class="badge">{{ refObj.database }}</span>
      <span class="divider-v"></span>
      <button class="btn" :disabled="loading || busy" @click="load">
        <Icon name="refresh" :size="13" /> 刷新
      </button>
      <button class="btn" :disabled="busy" @click="copyDefinition" title="复制完整索引结构（mappings / settings / aliases）">
        <Icon name="copy" :size="13" /> 复制索引结构
      </button>
      <button class="btn" :disabled="busy" @click="copyMapping" title="复制 mappings 部分">
        <Icon name="copy" :size="13" /> 复制 Mapping
      </button>
      <span class="grow"></span>
      <button class="btn btn-danger" :disabled="busy" @click="dropIndex">
        <Icon name="trash" :size="13" /> 删除索引
      </button>
    </div>

    <div v-if="error" class="alert alert-error">
      <Icon name="alert" :size="15" />
      <span class="grow" style="white-space: pre-wrap">{{ error }}</span>
      <button class="btn btn-icon" style="width: 20px; height: 20px" @click="error = ''"><Icon name="close" :size="12" /></button>
    </div>

    <div v-if="loading" class="loading-line" style="padding: 10px"><span class="spinner"></span> 正在读取索引结构…</div>

    <template v-else-if="indexDef">
      <div class="subtabs">
        <button class="subtab" :class="{ active: activeTab === 'fields' }" @click="activeTab = 'fields'">
          字段 ({{ fieldRows.length }})
        </button>
        <button class="subtab" :class="{ active: activeTab === 'settings' }" @click="activeTab = 'settings'">
          设置 ({{ settingsRows.length }})
        </button>
        <button class="subtab" :class="{ active: activeTab === 'aliases' }" @click="activeTab = 'aliases'">
          别名 ({{ aliasRows.length }})
        </button>
        <button class="subtab" :class="{ active: activeTab === 'ddl' }" @click="activeTab = 'ddl'">索引定义</button>
      </div>

      <div class="sv-body scroll">
        <template v-if="activeTab === 'fields'">
          <div class="scroll sv-table">
            <table class="data-table">
              <thead>
                <tr><th>字段路径</th><th>类型</th><th>子字段</th></tr>
              </thead>
              <tbody>
                <tr v-for="(f, i) in fieldRows" :key="i">
                  <td class="mono" :style="{ paddingLeft: 10 + f.depth * 16 + 'px' }">{{ f.path }}</td>
                  <td class="mono"><span class="badge badge-info">{{ f.type }}</span></td>
                  <td class="mono dim">{{ f.subfields.length ? f.subfields.map((s) => "." + s).join(" ") : "—" }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <EmptyState v-if="!fieldRows.length" icon="column" title="没有字段信息" :compact="true" />
        </template>

        <template v-else-if="activeTab === 'settings'">
          <div class="scroll sv-table">
            <table class="data-table">
              <thead><tr><th>配置项</th><th>值</th></tr></thead>
              <tbody>
                <tr v-for="(s, i) in settingsRows" :key="i">
                  <td class="mono">{{ s.key }}</td>
                  <td class="mono">{{ s.value }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <EmptyState v-if="!settingsRows.length" icon="settings" title="无可调设置" description="该索引未返回可回放的设置项。" :compact="true" />
        </template>

        <template v-else-if="activeTab === 'aliases'">
          <div class="scroll sv-table">
            <table class="data-table">
              <thead><tr><th>别名</th><th>配置</th></tr></thead>
              <tbody>
                <tr v-for="a in aliasRows" :key="a.name">
                  <td class="mono"><strong>{{ a.name }}</strong></td>
                  <td class="mono dim">{{ a.config }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <EmptyState v-if="!aliasRows.length" icon="link" title="无别名" description="该索引没有绑定别名。" :compact="true" />
        </template>

        <template v-else>
          <div class="ddl-box">
            <div class="row" style="margin-bottom: 8px">
              <Icon name="code" :size="14" />
              <strong style="font-size: 12px">索引定义（可重放的 PUT 脚本）</strong>
              <span class="grow"></span>
              <button class="btn btn-sm" @click="copyDefinition"><Icon name="copy" :size="12" /> 复制</button>
            </div>
            <pre class="code">{{ definition }}</pre>
          </div>
        </template>
      </div>
    </template>

    <EmptyState v-else icon="search" title="无法读取索引结构" description="请检查连接状态或该索引是否仍存在。" :compact="true">
      <button class="btn btn-sm" @click="load()">重试</button>
    </EmptyState>
  </div>
</template>

<style scoped>
.sv { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; padding: 8px; gap: 8px; overflow: hidden; }
.sv > .toolbar { border: 1px solid var(--border); border-radius: var(--radius); padding: 7px 9px; flex-shrink: 0; }
.subtabs { display: flex; gap: 4px; flex-shrink: 0; }
.subtab { padding: 6px 12px; border: 1px solid var(--border); border-bottom: none; border-radius: var(--radius-sm) var(--radius-sm) 0 0; background: var(--surface); color: var(--text-dim); font-size: 12px; cursor: pointer; }
.subtab.active { background: var(--surface-2); color: var(--text); font-weight: 600; }
.sv-body { flex: 1; min-height: 0; min-width: 0; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); overflow: auto; }
.sv-table { padding: 0; min-width: 0; }
.ddl-box { padding: 12px; }
.ddl-box pre { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 12px; font-size: 11px; line-height: 1.7; overflow: auto; white-space: pre-wrap; word-break: break-word; }
</style>
