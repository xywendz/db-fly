<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useWorkspace } from "@/stores/workspace";
import { runtimeMode, currentUser, logout } from "@/runtime";
import type { AuthUser } from "@/runtime/types";
import Icon from "./ui/Icon.vue";

const store = useWorkspace();
const conn = computed(() => store.connections.find((c) => c.name === store.activeConn));
const dotClass = computed(() => `status-dot ${store.status.level === "ok" ? "on" : store.status.level === "error" ? "err" : ""}`);
const user = ref<AuthUser | null>(null);

onMounted(async () => {
  if (runtimeMode === "web") user.value = await currentUser();
});

function signOut() {
  void logout();
}
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
    <template v-if="runtimeMode === 'web' && user">
      <span class="badge">用户 {{ user.username }}</span>
      <button type="button" class="sb-logout" @click="signOut">退出</button>
      <span class="divider-v"></span>
    </template>
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
.sb-logout {
  padding: 1px 7px; border: 1px solid var(--border); border-radius: 5px;
  background: transparent; color: var(--text-dim); font-size: 11px; cursor: pointer;
}
.sb-logout:hover { color: var(--text); border-color: #4c7dff; }
</style>
