<script setup lang="ts">
import { computed } from "vue";
import type { MenuItem } from "@/types/ui";
import Icon from "./Icon.vue";

const props = withDefaults(defineProps<{ open: boolean; x: number; y: number; items: MenuItem[] }>(), {
  x: 0,
  y: 0,
});
const emit = defineEmits<{ (e: "close"): void }>();

// 防止菜单超出窗口边界。
const pos = computed(() => {
  const w = 190;
  const h = Math.min(props.items.length * 32 + 12, 420);
  return {
    left: Math.max(4, Math.min(props.x, window.innerWidth - w - 8)) + "px",
    top: Math.max(4, Math.min(props.y, window.innerHeight - h - 8)) + "px",
  };
});

function pick(item: MenuItem) {
  if (item.disabled || item.separator) return;
  emit("close");
  item.action?.();
}
</script>

<template>
  <Teleport to="body">
    <template v-if="open">
      <div class="ctx-overlay" @click="emit('close')" @contextmenu.prevent="emit('close')"></div>
      <div class="ctx-menu" :style="pos">
        <template v-for="(item, i) in items" :key="i">
          <div v-if="item.separator" class="ctx-sep"></div>
          <button
            v-else
            class="ctx-item"
            :class="{ danger: item.danger }"
            :disabled="item.disabled"
            @click="pick(item)"
          >
            <Icon v-if="item.icon" :name="item.icon" :size="14" />
            <span v-else style="width: 14px"></span>
            <span class="grow">{{ item.label }}</span>
          </button>
        </template>
      </div>
    </template>
  </Teleport>
</template>

<style scoped>
.ctx-overlay { position: fixed; inset: 0; z-index: 149; }
</style>
