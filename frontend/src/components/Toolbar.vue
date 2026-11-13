<script setup lang="ts">
import { computed } from "vue";
import { useWorkspace } from "@/stores/workspace";
import Icon from "./ui/Icon.vue";

const store = useWorkspace();
const emit = defineEmits<{
  (e: "new-connection"): void;
  (e: "new-query"): void;
  (e: "new-table"): void;
  (e: "import"): void;
  (e: "export"): void;
  (e: "settings"): void;
  (e: "about"): void;
}>();

const connName = computed({
  get: () => store.activeConn,
  set: (v: string) => store.activate(v),
});

function newQuery() {
  if (store.connections.length) emit("new-query");
  else emit("new-connection");
}
</script>

<template>
  <div class="toolbar">
    <img src="/logo.png" alt="db-fly" class="tb-logo" />
    <strong class="tb-brand">db-fly</strong>
    <span class="divider-v"></span>

    <button class="btn" title="新建数据库连接" @click="emit('new-connection')">
      <Icon name="plus" :size="14" /> 连接
    </button>
    <button class="btn" title="新建查询 (Alt+N)" @click="newQuery">
      <Icon name="terminal" :size="14" /> 查询
    </button>
    <button class="btn" title="导入文件" :disabled="!store.connections.length" @click="emit('import')">
      <Icon name="upload" :size="14" /> 导入
    </button>
    <button class="btn" title="导出数据" :disabled="!store.connections.length" @click="emit('export')">
      <Icon name="download" :size="14" /> 导出
    </button>

    <span class="divider-v"></span>
    <span class="dim" style="font-size: 11px">连接</span>
    <select v-model="connName" style="min-width: 140px; max-width: 220px" :disabled="!store.connections.length">
      <option v-if="!store.connections.length" value="">（无连接）</option>
      <option v-for="c in store.connections" :key="c.name" :value="c.name">
        {{ c.name }} · {{ c.kind }}
      </option>
    </select>

    <span class="grow"></span>

    <button
      class="btn btn-icon"
      :class="{ active: !store.leftCollapsed }"
      :title="store.leftCollapsed ? '显示侧边栏' : '隐藏侧边栏'"
      @click="store.leftCollapsed = !store.leftCollapsed"
    >
      <Icon name="panel-left" :size="16" />
    </button>
    <button
      class="btn"
      :class="{ active: store.rightPanel === 'history' }"
      title="查询历史 (Alt+H)"
      @click="store.togglePanel('history')"
    >
      <Icon name="history" :size="14" /> 历史
    </button>
    <button
      class="btn"
      :class="{ active: store.rightPanel === 'favorites' }"
      title="收藏 (Alt+F)"
      @click="store.togglePanel('favorites')"
    >
      <Icon name="star" :size="14" /> 收藏
    </button>
    <button class="btn btn-icon" title="设置：字体 / 主题 / 分页大小" @click="emit('settings')">
      <Icon name="settings" :size="16" />
    </button>
    <button class="btn btn-icon" title="关于 db-fly" @click="emit('about')">
      <Icon name="info" :size="16" />
    </button>
  </div>
</template>

<style scoped>
.tb-logo { width: 24px; height: 24px; border-radius: 6px; }
.tb-brand { font-size: 14px; letter-spacing: -.02em; color: var(--accent-hover); margin-right: 2px; }
</style>
