<script setup lang="ts">
import { onMounted, onUnmounted } from "vue";
import Icon from "./Icon.vue";

const props = withDefaults(
  defineProps<{
    open: boolean;
    title: string;
    subtitle?: string;
    width?: string;
    /** 表单类弹窗可关闭遮罩关闭，避免误点弹窗外丢失已填内容。 */
    closeOnMask?: boolean;
    closeOnEsc?: boolean;
  }>(),
  { width: "480px", closeOnMask: true, closeOnEsc: true },
);
const emit = defineEmits<{ (e: "close"): void }>();

function onMaskClick() {
  if (props.closeOnMask) emit("close");
}
function onKey(e: KeyboardEvent) {
  if (e.key === "Escape" && props.open && props.closeOnEsc) {
    e.stopPropagation();
    emit("close");
  }
}
onMounted(() => window.addEventListener("keydown", onKey));
onUnmounted(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="modal-mask" @click.self="onMaskClick">
      <div class="modal" :style="{ width }" role="dialog" aria-modal="true">
        <div class="modal-head">
          <div class="grow">
            <h3>{{ title }}</h3>
            <p v-if="subtitle">{{ subtitle }}</p>
          </div>
          <button class="btn-icon" :title="closeOnEsc ? '关闭 (Esc)' : '关闭'" @click="emit('close')">
            <Icon name="close" />
          </button>
        </div>
        <div class="modal-body scroll">
          <slot />
        </div>
        <div v-if="$slots.footer" class="modal-foot">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </Teleport>
</template>
