<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{ name: string; size?: number | string; strokeWidth?: number | string }>(),
  { size: 16, strokeWidth: 1.8 },
);

// 统一 24×24 线性图标，避免引入外部图标库与网络资源。
const ICONS: Record<string, string[]> = {
  database: [
    "M12 3c4.42 0 8 1.12 8 2.5S16.42 8 12 8 4 6.88 4 5.5 7.58 3 12 3z",
    "M4 5.5v6c0 1.38 3.58 2.5 8 2.5s8-1.12 8-2.5v-6",
    "M4 11.5v6c0 1.38 3.58 2.5 8 2.5s8-1.12 8-2.5v-6",
  ],
  table: ["M3 4h18v16H3z", "M3 9h18", "M9 9v11", "M15 9v11"],
  view: [
    "M2 12s3.6-6 10-6 10 6 10 6-3.6 6-10 6-10-6-10-6z",
    "M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z",
  ],
  key: ["M14 7a4 4 0 1 1-3.6 5.7L3 20v-3l3-3v-3l3-3h2.3A4 4 0 0 1 14 7z", "M15.5 9.5h.01"],
  search: ["M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z", "M16 16l4 4"],
  plus: ["M12 5v14", "M5 12h14"],
  minus: ["M5 12h14"],
  refresh: ["M20 11a8 8 0 0 0-14-4.5L4 9", "M4 4v5h5", "M4 13a8 8 0 0 0 14 4.5L20 15", "M20 20v-5h-5"],
  play: ["M7 4l12 8-12 8z"],
  code: ["M9 7l-5 5 5 5", "M15 7l5 5-5 5"],
  terminal: ["M4 5h16v14H4z", "M7 9l3 3-3 3", "M13 15h4"],
  history: ["M3 12a9 9 0 1 0 3-6.7L3 8", "M3 3v5h5", "M12 7v5l3 2"],
  star: ["M12 3l2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3 6.4 20.2l1.1-6.2L3 9.6l6.2-.9z"],
  info: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z", "M12 11v5", "M12 8h.01"],
  close: ["M6 6l12 12", "M18 6L6 18"],
  check: ["M5 12l4 4L19 7"],
  "chevron-right": ["M9 5l7 7-7 7"],
  "chevron-down": ["M5 9l7 7 7-7"],
  "chevron-left": ["M15 5l-7 7 7 7"],
  "arrow-left": ["M19 12H5", "M11 6l-6 6 6 6"],
  "arrow-right": ["M5 12h14", "M13 6l6 6-6 6"],
  "chevrons-left": ["M17 5l-7 7 7 7", "M11 5l-7 7 7 7"],
  "chevrons-right": ["M7 5l7 7-7 7", "M13 5l7 7-7 7"],
  trash: ["M4 7h16", "M9 7V4h6v3", "M6 7l1 13h10l1-13", "M10 11v6", "M14 11v6"],
  edit: ["M4 20h4l11-11-4-4L4 16z", "M13 7l4 4"],
  save: ["M4 4h13l3 3v13H4z", "M8 4v6h8V4", "M8 20v-6h8v6"],
  upload: ["M12 16V4", "M7 9l5-5 5 5", "M4 16v4h16v-4"],
  download: ["M12 4v12", "M7 11l5 5 5-5", "M4 20h16"],
  copy: ["M9 9h11v11H9z", "M5 15H4V4h11v1"],
  filter: ["M3 5h18l-7 8v6l-4 2v-8z"],
  sort: ["M7 4v16", "M4 7l3-3 3 3", "M17 20V4", "M14 17l3 3 3-3"],
  alert: ["M12 3L2 20h20z", "M12 9v5", "M12 17h.01"],
  server: ["M3 4h18v6H3z", "M3 14h18v6H3z", "M7 7h.01", "M7 17h.01"],
  column: ["M4 4h16v16H4z", "M10 4v16", "M14 4v16"],
  index: ["M4 6h16", "M4 12h10", "M4 18h7", "M18 14v6", "M15 17h6"],
  link: ["M10 13a5 5 0 0 0 7 0l2-2a5 5 0 0 0-7-7l-1 1", "M14 11a5 5 0 0 0-7 0l-2 2a5 5 0 0 0 7 7l1-1"],
  more: ["M5 12h.01", "M12 12h.01", "M19 12h.01"],
  "panel-left": ["M3 4h18v16H3z", "M9 4v16"],
  "panel-right": ["M3 4h18v16H3z", "M15 4v16"],
  file: ["M6 3h8l4 4v14H6z", "M14 3v5h5", "M9 13h6", "M9 17h6"],
  clock: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z", "M12 7v5l3 2"],
  bolt: ["M13 2L4 14h7l-1 8 9-12h-7z"],
  eye: ["M2 12s3.6-6 10-6 10 6 10 6-3.6 6-10 6-10-6-10-6z", "M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z"],
  shield: ["M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z", "M9 12l2 2 4-4"],
  folder: ["M3 6h6l2 2h10v11H3z"],
  sliders: ["M4 7h10", "M18 7h2", "M4 17h4", "M12 17h8", "M14 4v6", "M8 14v6"],
  layers: ["M12 3l9 5-9 5-9-5z", "M3 13l9 5 9-5", "M3 17l9 5 9-5"],
  list: ["M8 6h13", "M8 12h13", "M8 18h13", "M3 6h.01", "M3 12h.01", "M3 18h.01"],
  grid: ["M4 4h7v7H4z", "M13 4h7v7h-7z", "M4 13h7v7H4z", "M13 13h7v7h-7z"],
  settings: ["M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z", "M12 2l1.4 2.3 2.6.5.4 2.6 2.1 1.6-1 2.5 1 2.5-2.1 1.6-.4 2.6-2.6.5L12 22l-1.4-2.3-2.6-.5-.4-2.6L5.5 15l1-2.5-1-2.5 2.1-1.6.4-2.6 2.6-.5z"],
  circle: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"],
};

const paths = computed(() => ICONS[props.name] ?? ICONS.circle);
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    style="flex-shrink: 0"
  >
    <path v-for="(d, i) in paths" :key="i" :d="d" />
  </svg>
</template>
