<script setup lang="ts">
import { computed } from "vue";
import { useWorkspace } from "@/stores/workspace";
import Icon from "./ui/Icon.vue";

const store = useWorkspace();
const conn = computed(() => store.connections.find((c) => c.name === store.activeConn));
const dotClass = computed(() => `status-dot ${store.status.level === "ok" ? "on" : store.status.level === "error" ? "err" : ""}`);
</script>

<template>
  <div class="statusbar">
    <span :class="dotClass"></span>
    <span class="sb-msg truncate">{{ store.status.text }}</span>
    <span class="divider-v"></span>
    <template v-if="conn">
      <Icon name="database" :size="13" />
      <span class="truncate">{{ conn.name }}</span>
      <span class="badge">{{ conn.kind }}</span>
      <span v-if="conn.read_only" class="badge badge-warn">只读</span>
      <span v-if="store.activeDb" class="dim truncate" style="max-width: 160px">· {{ store.activeDb }}</span>
    </template>
    <span class="grow"></span>
    <span class="dim nowrap">页签 {{ store.tabs.length }}</span>
    <span class="divider-v"></span>
    <span class="dim nowrap"><span class="kbd">Alt+N</span> 查询 · <span class="kbd">Alt+W</span> 关闭</span>
  </div>
</template>

<style scoped>
.statusbar {
  display: flex; align-items: center; gap: 8px; padding: 5px 12px;
  background: var(--surface); border-top: 1px solid var(--border);
  font-size: 11px; color: var(--text-dim); flex-shrink: 0; height: 28px;
}
.sb-msg { max-width: 42%; color: var(--text); }
</style>
