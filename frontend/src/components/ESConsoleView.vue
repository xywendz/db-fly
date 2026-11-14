<script setup lang="ts">
import { computed, ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { QueryOutcome } from "@/types/domain";
import ResultGrid from "./ResultGrid.vue";
import Icon from "./ui/Icon.vue";

// Elasticsearch 没有「数据库」概念：一次请求 = HTTP 方法 + URL（索引名 + action，如 /shop.users/_search）
// + 请求体（DSL）。本控制台按该模型拆分输入，比通用 SQL 查询页更贴合 ES。
const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const methods = ["GET", "PUT", "POST", "DELETE", "HEAD", "PATCH"] as const;
const method = ref<(typeof methods)[number]>("GET");
const path = ref("");
const body = ref("");
const outcome = ref<QueryOutcome | null>(null);
const loading = ref(false);
const error = ref("");

const conn = computed(() => store.connections.find((c) => c.name === props.tab.conn));

const examples = [
  {
    label: "搜索示例",
    method: "POST" as const,
    path: "shop.users/_search",
    body: '{\n  "query": { "match_all": {} },\n  "size": 10\n}',
  },
  {
    label: "创建索引示例",
    method: "PUT" as const,
    path: "shop.users",
    body:
      '{\n' +
      '  "settings": { "number_of_shards": 1, "number_of_replicas": 1 },\n' +
      '  "mappings": {\n' +
      '    "properties": {\n' +
      '      "name": { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 256 } } }\n' +
      "    }\n" +
      "  }\n" +
      "}",
  },
  { label: "查看所有索引", method: "GET" as const, path: "_cat/indices?v", body: "" },
];

function applyExample(ex: (typeof examples)[number]) {
  method.value = ex.method;
  path.value = ex.path;
  body.value = ex.body;
  error.value = "";
}

function formatBody() {
  const t = body.value.trim();
  if (!t) return;
  try {
    body.value = JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    store.setStatus("Body 不是合法 JSON，无法格式化", "warn");
  }
}

async function run() {
  const p = path.value.trim().replace(/^\//, "");
  if (!p) {
    error.value = "请填写请求路径（索引名 + 可选 action，例如 shop.users/_search）";
    return;
  }
  loading.value = true;
  error.value = "";
  outcome.value = null;
  const stmt = method.value.toUpperCase() + " /" + p + (body.value.trim() ? "\n" + body.value.trim() : "");
  try {
    outcome.value = await api.executeQuery({
      conn: props.tab.conn,
      query: { language: "es_dsl", text: stmt },
    });
    store.setStatus("执行完成", "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`执行失败：${error.value}`, "error");
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="es-console">
    <div class="conn-bar">
      <Icon name="search" :size="14" />
      <span class="conn-name">{{ conn?.name }}</span>
      <span class="conn-kind">Elasticsearch · 控制台</span>
      <span class="hint">无数据库概念：URL 含索引名 + action，body 为 DSL</span>
    </div>

    <div class="req-bar">
      <select v-model="method" class="method">
        <option v-for="m in methods" :key="m" :value="m">{{ m }}</option>
      </select>
      <input
        v-model="path"
        class="path mono"
        placeholder="shop.users 或 shop.users/_search 或 _cat/indices?v"
        @keydown.ctrl.enter="run"
        @keydown.meta.enter="run"
      />
      <button class="btn btn-sm btn-primary" :disabled="loading" @click="run">
        <Icon name="play" :size="12" /> 执行
      </button>
      <button class="btn btn-sm" :disabled="!body.trim()" @click="formatBody">格式化</button>
    </div>

    <div class="examples">
      <span class="ex-label">示例：</span>
      <button v-for="ex in examples" :key="ex.label" class="chip" @click="applyExample(ex)">
        {{ ex.label }}
      </button>
    </div>

    <textarea
      v-model="body"
      class="mono body"
      :placeholder="method === 'GET' || method === 'HEAD' ? 'GET/HEAD 通常无 body，可留空' : 'DSL 请求体（JSON）'"
      spellcheck="false"
    ></textarea>

    <p v-if="error" class="err">{{ error }}</p>
    <ResultGrid v-if="outcome" :outcome="outcome" />
  </div>
</template>

<style scoped>
.es-console {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: 12px 16px;
  gap: 10px;
}
.conn-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-dim);
  font-size: 13px;
}
.conn-name {
  color: var(--text);
  font-weight: 600;
}
.conn-kind {
  color: var(--accent);
}
.hint {
  margin-left: auto;
  font-style: italic;
}
.req-bar {
  display: flex;
  gap: 8px;
  align-items: center;
}
.method {
  width: 96px;
}
.path {
  flex: 1;
}
.examples {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.ex-label {
  color: var(--text-dim);
  font-size: 12px;
}
.chip {
  border: 1px solid var(--border);
  background: var(--bg-2);
  color: var(--text);
  border-radius: 12px;
  padding: 2px 10px;
  font-size: 12px;
  cursor: pointer;
}
.chip:hover {
  border-color: var(--accent);
}
.body {
  flex: 1;
  min-height: 160px;
  resize: vertical;
  font-size: 13px;
}
.err {
  color: var(--danger);
  white-space: pre-wrap;
}
</style>
