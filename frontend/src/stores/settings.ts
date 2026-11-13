import { defineStore } from "pinia";
import { ref, watch } from "vue";

export type ThemeName = "dark" | "light" | "midnight";
export type DensityName = "comfortable" | "compact";

export interface UISettings {
  theme: ThemeName;
  /** 基础字号（px），全局 11/12/13px 控件字号按此缩放。 */
  fontSize: number;
  fontFamily: string;
  monoFamily: string;
  /** SQL / 代码编辑器字号（px）。 */
  codeFontSize: number;
  density: DensityName;
  /** 表数据页默认每页行数。 */
  pageSize: number;
  /** 危险操作是否弹出二次确认。 */
  confirmDangerous: boolean;
}

export const THEME_OPTIONS: { value: ThemeName; label: string; hint: string }[] = [
  { value: "dark", label: "暗色", hint: "默认 · 低亮度护眼" },
  { value: "light", label: "浅色", hint: "明亮环境 / 投屏" },
  { value: "midnight", label: "午夜蓝", hint: "更深底色 · 高对比" },
];

export const FONT_OPTIONS: { value: string; label: string; stack: string }[] = [
  { value: "system", label: "系统默认", stack: '"Segoe UI", "Microsoft YaHei", -apple-system, system-ui, sans-serif' },
  { value: "yahei", label: "微软雅黑", stack: '"Microsoft YaHei", "PingFang SC", "Noto Sans SC", sans-serif' },
  { value: "segoe", label: "Segoe UI", stack: '"Segoe UI", "Helvetica Neue", Arial, sans-serif' },
  { value: "noto", label: "思源黑体 / 苹方", stack: '"Noto Sans SC", "Source Han Sans SC", "PingFang SC", sans-serif' },
  { value: "sans", label: "无衬线 Arial", stack: "Arial, Helvetica, sans-serif" },
];

export const MONO_OPTIONS: { value: string; label: string; stack: string }[] = [
  { value: "cascadia", label: "Cascadia Code", stack: '"Cascadia Code", "Cascadia Mono", Consolas, monospace' },
  { value: "jetbrains", label: "JetBrains Mono", stack: '"JetBrains Mono", "Cascadia Code", Consolas, monospace' },
  { value: "consolas", label: "Consolas", stack: 'Consolas, "Courier New", monospace' },
  { value: "courier", label: "Courier New", stack: '"Courier New", monospace' },
];

export const DEFAULT_SETTINGS: UISettings = {
  theme: "dark",
  fontSize: 14,
  fontFamily: "system",
  monoFamily: "cascadia",
  codeFontSize: 13,
  density: "comfortable",
  pageSize: 200,
  confirmDangerous: true,
};

const STORAGE_KEY = "db-fly.settings.v1";

function clamp(value: unknown, min: number, max: number, fallback: number): number {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallback;
  return Math.max(min, Math.min(max, Math.round(n)));
}

function load(): UISettings {
  let raw: Partial<UISettings> = {};
  try {
    raw = JSON.parse(localStorage.getItem(STORAGE_KEY) || "{}") || {};
  } catch {
    raw = {};
  }
  return {
    theme: THEME_OPTIONS.some((o) => o.value === raw.theme) ? (raw.theme as ThemeName) : DEFAULT_SETTINGS.theme,
    fontSize: clamp(raw.fontSize, 12, 20, DEFAULT_SETTINGS.fontSize),
    fontFamily: FONT_OPTIONS.some((o) => o.value === raw.fontFamily) ? raw.fontFamily! : DEFAULT_SETTINGS.fontFamily,
    monoFamily: MONO_OPTIONS.some((o) => o.value === raw.monoFamily) ? raw.monoFamily! : DEFAULT_SETTINGS.monoFamily,
    codeFontSize: clamp(raw.codeFontSize, 11, 20, DEFAULT_SETTINGS.codeFontSize),
    density: raw.density === "compact" ? "compact" : "comfortable",
    pageSize: clamp(raw.pageSize, 10, 5000, DEFAULT_SETTINGS.pageSize),
    confirmDangerous: raw.confirmDangerous !== false,
  };
}

function persist(s: UISettings) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
  } catch {
    /* localStorage 不可用时忽略 */
  }
}

/** 把设置写入根元素：主题/密度用 data-*，字体与字号用 CSS 变量覆盖。 */
export function applySettings(s: UISettings) {
  const root = document.documentElement;
  root.dataset.theme = s.theme;
  root.dataset.density = s.density;
  root.style.setProperty("--font", FONT_OPTIONS.find((o) => o.value === s.fontFamily)?.stack ?? FONT_OPTIONS[0].stack);
  root.style.setProperty("--mono", MONO_OPTIONS.find((o) => o.value === s.monoFamily)?.stack ?? MONO_OPTIONS[0].stack);
  root.style.setProperty("--fs-base", `${clamp(s.fontSize, 12, 20, DEFAULT_SETTINGS.fontSize)}px`);
  root.style.setProperty("--fs-code", `${clamp(s.codeFontSize, 11, 20, DEFAULT_SETTINGS.codeFontSize)}px`);
}

/** 应用启动前调用，避免首屏闪烁。 */
export function initSettings() {
  applySettings(load());
}

export const useSettings = defineStore("settings", () => {
  const state = ref<UISettings>(load());

  watch(
    state,
    (v) => {
      applySettings(v);
      persist(v);
    },
    { deep: true },
  );

  function update(patch: Partial<UISettings>) {
    state.value = { ...state.value, ...patch };
  }
  function replace(next: UISettings) {
    state.value = { ...next };
  }
  function reset() {
    state.value = { ...DEFAULT_SETTINGS };
  }
  return { state, update, replace, reset, apply: () => applySettings(state.value) };
});
