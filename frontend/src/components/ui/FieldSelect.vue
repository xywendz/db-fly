<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    fields: string[];
    placeholder?: string;
    width?: string;
  }>(),
  { placeholder: "搜索字段…", width: "160px" },
);
const emit = defineEmits<{ (e: "update:modelValue", value: string): void }>();

interface FieldResult {
  field: string;
  hits: boolean[];
  score: number;
}

// 模糊匹配：精确 > 前缀 > 子串 > 子序列；hits 记录命中字符用于高亮，score 越小越靠前。
function fuzzyMatch(text: string, q: string): FieldResult | null {
  const lt = text.toLowerCase();
  const lq = q.toLowerCase();
  const hits = new Array<boolean>(text.length).fill(false);
  if (lt === lq) {
    hits.fill(true);
    return { field: text, hits, score: 0 };
  }
  if (lt.startsWith(lq)) {
    for (let i = 0; i < lq.length; i++) hits[i] = true;
    return { field: text, hits, score: 10 };
  }
  const sub = lt.indexOf(lq);
  if (sub >= 0) {
    for (let i = 0; i < lq.length; i++) hits[sub + i] = true;
    return { field: text, hits, score: 20 + sub };
  }
  let qi = 0;
  let prev = -1;
  let gap = 0;
  let first = -1;
  for (let ti = 0; ti < lt.length && qi < lq.length; ti++) {
    if (lt[ti] === lq[qi]) {
      hits[ti] = true;
      if (first < 0) first = ti;
      if (prev >= 0) gap += ti - prev - 1;
      prev = ti;
      qi++;
    }
  }
  if (qi < lq.length) return null;
  return { field: text, hits, score: 100 + gap + first };
}

const root = ref<HTMLElement | null>(null);
const input = ref<HTMLInputElement | null>(null);
const open = ref(false);
const query = ref("");
const activeIndex = ref(0);

const results = computed<FieldResult[]>(() => {
  const q = query.value.trim();
  if (!q) return props.fields.map((field) => ({ field, hits: [], score: 0 }));
  const matched: FieldResult[] = [];
  for (const field of props.fields) {
    const m = fuzzyMatch(field, q);
    if (m) matched.push(m);
  }
  matched.sort((a, b) => a.score - b.score || a.field.localeCompare(b.field));
  return matched;
});

function segments(r: FieldResult): { text: string; hit: boolean }[] {
  const out: { text: string; hit: boolean }[] = [];
  let cur = "";
  let curHit = false;
  for (let i = 0; i < r.field.length; i++) {
    const hit = r.hits[i] === true;
    if (cur && hit !== curHit) {
      out.push({ text: cur, hit: curHit });
      cur = "";
    }
    curHit = hit;
    cur += r.field[i];
  }
  if (cur) out.push({ text: cur, hit: curHit });
  return out;
}

watch(() => props.modelValue, (v) => { if (!open.value) query.value = v; }, { immediate: true });

function openList() {
  if (open.value) return;
  open.value = true;
  query.value = "";
  activeIndex.value = Math.max(0, props.fields.indexOf(props.modelValue));
  nextTick(() => input.value?.select());
}
function choose(field: string) {
  emit("update:modelValue", field);
  query.value = field;
  open.value = false;
  input.value?.blur();
}
function onInput(e: Event) {
  query.value = (e.target as HTMLInputElement).value;
  if (!open.value) open.value = true;
  activeIndex.value = 0;
}
function onKeydown(e: KeyboardEvent) {
  if (e.key === "ArrowDown") {
    e.preventDefault();
    if (!open.value) openList();
    activeIndex.value = Math.min(activeIndex.value + 1, Math.max(0, results.value.length - 1));
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    activeIndex.value = Math.max(activeIndex.value - 1, 0);
  } else if (e.key === "Enter" && open.value) {
    e.preventDefault();
    const item = results.value[activeIndex.value];
    if (item) choose(item.field);
  } else if (e.key === "Escape") {
    open.value = false;
    query.value = props.modelValue;
    input.value?.blur();
  }
}
function close() {
  open.value = false;
  query.value = props.modelValue;
}
function onDocDown(e: MouseEvent) {
  if (open.value && root.value && !root.value.contains(e.target as Node)) close();
}
onMounted(() => document.addEventListener("mousedown", onDocDown));
onBeforeUnmount(() => document.removeEventListener("mousedown", onDocDown));
</script>

<template>
  <div ref="root" class="field-select" :style="{ width }">
    <div class="fs-control" :class="{ open }" @click="openList">
      <Icon name="search" :size="12" />
      <input
        ref="input"
        :value="open ? query : modelValue"
        :placeholder="modelValue ? `当前：${modelValue}` : placeholder"
        @focus="openList"
        @input="onInput"
        @keydown="onKeydown"
        @blur="close"
      />
      <Icon name="chevron-down" :size="12" />
    </div>
    <div v-if="open" class="fs-menu">
      <button
        v-for="(item, i) in results"
        :key="item.field"
        type="button"
        class="fs-option"
        :class="{ active: i === activeIndex }"
        @mouseenter="activeIndex = i"
        @mousedown.prevent="choose(item.field)"
      >
        <Icon name="column" :size="11" />
        <span class="grow truncate">
          <span v-for="(seg, si) in segments(item)" :key="si" :class="{ hit: seg.hit }">{{ seg.text }}</span>
        </span>
        <Icon v-if="item.field === modelValue" name="check" :size="11" />
      </button>
      <div v-if="!results.length" class="fs-empty">无匹配字段</div>
    </div>
  </div>
</template>

<style scoped>
.field-select { position: relative; }
.fs-control {
  display: flex; align-items: center; gap: 5px; height: 28px; padding: 0 7px;
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-sm);
  color: var(--text-dim); cursor: text;
}
.fs-control:hover { border-color: var(--border-strong); }
.fs-control.open { border-color: var(--accent); box-shadow: 0 0 0 3px var(--accent-soft); }
.fs-control input {
  flex: 1; min-width: 0; padding: 0; border: 0; background: transparent;
  font-size: var(--fs-sm); box-shadow: none;
}
.fs-control input:focus { border: 0; box-shadow: none; outline: none; }
.fs-menu {
  position: absolute; z-index: 40; top: calc(100% + 3px); left: 0; right: 0;
  max-height: 224px; overflow: auto; padding: 3px;
  background: var(--surface); border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm); box-shadow: 0 8px 24px rgba(0, 0, 0, .3);
}
.fs-option {
  display: flex; align-items: center; gap: 6px; width: 100%; padding: 5px 6px;
  background: transparent; border: 0; border-radius: 4px; color: var(--text);
  font-size: var(--fs-sm); text-align: left; cursor: pointer;
}
.fs-option.active, .fs-option:hover { background: var(--accent-soft); color: var(--accent-hover); }
.fs-option .hit { color: var(--accent); font-weight: 600; }
.fs-empty { padding: 8px 6px; font-size: var(--fs-xs); color: var(--text-faint); text-align: center; }
</style>
