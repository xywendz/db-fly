<script setup lang="ts">
import { ref } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ObjectRef, QueryOutcome } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const db = props.tab.database ?? props.tab.ref?.database ?? "";
const pattern = ref("");
const maxRows = ref(200);
const outcome = ref<QueryOutcome | null>(null);
const loading = ref(false);
const error = ref("");
const searched = ref(false);

// 始终保留左侧资源管理器，方便切换对象。
store.leftCollapsed = false;

async function search() {
  loading.value = true;
  error.value = "";
  searched.value = true;
  try {
    const res = await api.executeQuery({
      conn: props.tab.conn,
      query: {
        language: "sql",
        ref: { database: db, name: "" } as ObjectRef,
        // filter 作为 SCAN 的 MATCH 模式；后端用游标分批扫描，不会阻塞服务端。
        filter: pattern.value.trim(),
        max_rows: maxRows.value,
      },
    });
    if (!res.pages.length) res.pages.push({ columns: [], rows: [] });
    outcome.value = res;
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  } finally {
    loading.value = false;
  }
}

function openKey(key: string) {
  if (!key) return;
  store.openDataTab(props.tab.conn, { database: db, name: key });
}
</script>

<template>
  <div class="rk-wrap">
    <div class="rk-bar card">
      <div class="rk-search">
        <Icon name="search" :size="14" />
        <input
          v-model="pattern"
          class="grow"
          placeholder="键匹配模式，如 user:* （留空匹配全部，上限由每页数控制）"
          @keydown.enter="search"
        />
      </div>
      <label class="rk-limit">
        上限
        <input
          type="number"
          :value="maxRows"
          min="10"
          max="5000"
          style="width: 72px"
          @change="(e) => (maxRows = Math.max(10, Math.min(5000, Number((e.target as HTMLInputElement).value) || 200)))"
        />
      </label>
      <button class="btn btn-primary btn-sm" :disabled="loading" @click="search">
        <Icon name="search" :size="13" /> 搜索
      </button>
      <button class="btn btn-sm" @click="(pattern = ''), (outcome = null), (searched = false)">清空</button>
    </div>

    <div v-if="error" class="rk-error">{{ error }}</div>

    <div v-if="!searched" class="rk-body scroll">
      <EmptyState
        icon="key"
        title="按模式检索 Redis 键"
        description="为避免在键数量庞大的库上拉取全部键，默认不加载。请输入匹配模式后点击「搜索」（基于 SCAN 游标分批扫描，不会阻塞服务端）。"
      />
    </div>

    <div v-else-if="loading" class="rk-body scroll">
      <div class="loading-line" style="padding: 10px"><span class="spinner"></span> 正在扫描键…</div>
    </div>

    <div v-else-if="(outcome?.pages[0]?.rows?.length ?? 0) === 0" class="rk-body scroll">
      <EmptyState icon="search" title="没有匹配的键" description="换个匹配模式再试，例如 user:* 或 *:session。" :compact="true" />
    </div>

    <div v-else class="rk-body scroll">
      <div class="faint" style="padding: 6px 10px; font-size: 11px">
        共 {{ outcome!.pages[0]!.rows!.length }} 个键（受上限约束）· 双击某行可打开该键查看内容
      </div>
      <table class="data-table">
        <thead>
          <tr>
            <th v-for="c in outcome!.pages[0]!.columns" :key="c.name">{{ c.name }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in outcome!.pages[0]!.rows"
            :key="(row[0]?.text ?? '') + i"
            class="rk-row"
            :title="`双击打开 ${row[0]?.text ?? ''}`"
            @dblclick="openKey(row[0]?.text ?? '')"
          >
            <td v-for="(cell, ci) in row" :key="ci" class="mono">{{ cell?.text ?? "" }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.rk-wrap { display: flex; flex-direction: column; height: 100%; background: var(--surface); }
.rk-bar { display: flex; align-items: center; gap: 8px; margin: 8px; padding: 8px; flex-wrap: wrap; }
.rk-search { display: flex; align-items: center; gap: 6px; flex: 1; min-width: 240px; color: var(--text-dim); }
.rk-search input { background: transparent; border: 0; padding: 2px 0; box-shadow: none; width: 100%; }
.rk-search input:focus { box-shadow: none; border: 0; }
.rk-limit { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-dim); }
.rk-error { margin: 0 8px 8px; padding: 8px 10px; border-radius: 6px; background: color-mix(in srgb, var(--danger) 14%, transparent); color: var(--danger); font-size: 12px; }
.rk-body { flex: 1; min-height: 0; }
.rk-row { cursor: pointer; }
.rk-row:hover { background: var(--surface-2); }
</style>
