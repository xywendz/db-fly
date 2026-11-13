<script setup lang="ts">
import { computed, ref, watch, nextTick } from "vue";
import { useWorkspace } from "@/stores/workspace";
import Modal from "./Modal.vue";
import Icon from "./Icon.vue";

const store = useWorkspace();
const state = computed(() => store.confirmState);
const input = ref("");
const inputEl = ref<HTMLInputElement | null>(null);

watch(
  () => state.value.open,
  async (open) => {
    if (!open) return;
    input.value = state.value.inputValue;
    await nextTick();
    inputEl.value?.focus();
    inputEl.value?.select();
  },
);

const showInput = computed(() => state.value.mode === "prompt" || !!state.value.requireText);
const canConfirm = computed(() => {
  if (state.value.mode === "prompt") return input.value.trim().length > 0;
  if (state.value.requireText) return input.value === state.value.requireText;
  return true;
});

function accept() {
  if (!canConfirm.value) return;
  store.resolveConfirm(state.value.mode === "prompt" ? input.value : true);
}
function dismiss() {
  store.resolveConfirm(state.value.mode === "prompt" ? null : false);
}
</script>

<template>
  <Modal :open="state.open" :title="state.title" width="440px" :close-on-mask="false" @close="dismiss">
    <div class="col" style="gap: 14px">
      <div v-if="state.subject" class="confirm-subject">
        <Icon name="table" :size="14" />
        <span class="dim">目标对象</span>
        <span class="subject-name">{{ state.subject }}</span>
      </div>
      <div v-if="state.message" class="row" style="align-items: flex-start; gap: 10px">
        <span v-if="state.danger" style="color: var(--danger); margin-top: 2px"><Icon name="alert" :size="18" /></span>
        <p style="line-height: 1.7; white-space: pre-wrap">{{ state.message }}</p>
      </div>
      <div v-if="showInput" class="field">
        <label class="field-label">
          {{ state.inputLabel || (state.requireText ? `请输入「${state.requireText}」以确认此操作` : "") }}
        </label>
        <input
          ref="inputEl"
          v-model="input"
          :placeholder="state.inputPlaceholder || state.requireText"
          @keydown.enter.prevent="accept"
        />
      </div>
    </div>
    <template #footer>
      <span class="grow"></span>
      <button class="btn" @click="dismiss">{{ state.cancelText }}</button>
      <button
        class="btn"
        :class="state.danger ? 'btn-danger' : 'btn-primary'"
        :disabled="!canConfirm"
        @click="accept"
      >
        {{ state.confirmText }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.confirm-subject { display: flex; align-items: center; gap: 8px; padding: 9px 12px; border: 1px solid var(--danger-soft); background: var(--danger-soft); border-radius: var(--radius); font-size: 12px; }
.confirm-subject .subject-name { font-family: var(--mono); font-weight: 700; font-size: 13px; color: var(--danger); }
</style>
