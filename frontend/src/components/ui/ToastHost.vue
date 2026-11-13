<script setup lang="ts">
import { useWorkspace } from "@/stores/workspace";
import Icon from "./Icon.vue";

const store = useWorkspace();
const iconOf: Record<string, string> = {
  info: "info",
  ok: "check",
  warn: "alert",
  error: "alert",
};
</script>

<template>
  <Teleport to="body">
    <div class="toast-host">
      <div
        v-for="t in store.toasts"
        :key="t.id"
        class="toast"
        :class="`toast-${t.level}`"
        @click="store.dismissToast(t.id)"
      >
        <span style="margin-top: 1px"><Icon :name="iconOf[t.level] || 'info'" :size="15" /></span>
        <span class="grow">{{ t.text }}</span>
        <button class="btn-icon" style="width: 18px; height: 18px; padding: 0" title="关闭">
          <Icon name="close" :size="12" />
        </button>
      </div>
    </div>
  </Teleport>
</template>
