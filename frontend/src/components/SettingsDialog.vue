<script setup lang="ts">
import { ref, watch } from "vue";
import {
  applySettings,
  DEFAULT_SETTINGS,
  FONT_OPTIONS,
  MONO_OPTIONS,
  THEME_OPTIONS,
  useSettings,
  type UISettings,
} from "@/stores/settings";
import * as api from "@/api/client";
import Modal from "./ui/Modal.vue";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ (e: "close"): void; (e: "saved"): void }>();

const settings = useSettings();
const draft = ref<UISettings>({ ...settings.state });

/** 编辑时即时预览；取消 / 关闭时恢复已保存的设置。 */
watch(draft, (v) => applySettings(v), { deep: true });
watch(
  () => props.open,
  (open) => {
    if (open) draft.value = { ...settings.state };
    else applySettings(settings.state);
  },
);

function cancel() {
  applySettings(settings.state);
  emit("close");
}
function restoreDefaults() {
  draft.value = { ...DEFAULT_SETTINGS };
}

/** 后端偏好同步失败时静默降级（浏览器预览 / 无后端场景）。 */
async function syncBackend(next: UISettings) {
  try {
    const p = await api.listPreferences();
    await api.savePreferences({
      ...p,
      max_rows: next.pageSize,
      theme: next.theme,
      confirm_dangerous: next.confirmDangerous,
    });
  } catch {
    /* 忽略 */
  }
}

function save() {
  const next: UISettings = { ...draft.value };
  settings.replace(next);
  emit("saved");
  emit("close");
  void syncBackend(next);
}
</script>

<template>
  <Modal
    :open="open"
    title="设置"
    subtitle="外观、字体与数据浏览偏好 · 修改时即时预览"
    width="640px"
    :close-on-mask="false"
    @close="cancel"
  >
    <div class="col" style="gap: 18px">
      <section>
        <div class="section-title"><Icon name="sliders" :size="12" /> 外观</div>
        <div class="field">
          <label class="field-label">主题</label>
          <div class="theme-row">
            <button
              v-for="o in THEME_OPTIONS"
              :key="o.value"
              class="theme-card"
              :class="{ active: draft.theme === o.value }"
              @click="draft.theme = o.value"
            >
              <span class="swatch" :class="`sw-${o.value}`"></span>
              <span class="col" style="gap: 2px; align-items: flex-start">
                <strong>{{ o.label }}</strong>
                <small class="faint">{{ o.hint }}</small>
              </span>
              <Icon v-if="draft.theme === o.value" name="check" :size="14" class="theme-check" />
            </button>
          </div>
        </div>

        <div class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label class="field-label">基础字号 <span class="badge badge-accent">{{ draft.fontSize }} px</span></label>
            <input v-model.number="draft.fontSize" type="range" min="12" max="20" step="1" />
            <span class="field-hint">作用于按钮、输入框、表格、标签等界面文字</span>
          </div>
          <div class="field">
            <label class="field-label">SQL 编辑器字号 <span class="badge badge-accent">{{ draft.codeFontSize }} px</span></label>
            <input v-model.number="draft.codeFontSize" type="range" min="11" max="20" step="1" />
            <span class="field-hint">仅影响 SQL 编辑区与行号</span>
          </div>
          <div class="field">
            <label class="field-label">界面字体</label>
            <select v-model="draft.fontFamily">
              <option v-for="o in FONT_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </div>
          <div class="field">
            <label class="field-label">等宽字体</label>
            <select v-model="draft.monoFamily">
              <option v-for="o in MONO_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </div>
          <div class="field">
            <label class="field-label">界面密度</label>
            <select v-model="draft.density">
              <option value="comfortable">舒适（默认间距）</option>
              <option value="compact">紧凑（一屏显示更多行）</option>
            </select>
          </div>
        </div>
      </section>

      <div class="divider-h"></div>

      <section>
        <div class="section-title"><Icon name="database" :size="12" /> 数据与操作</div>
        <div class="form-grid">
          <div class="field">
            <label class="field-label">数据页每页行数</label>
            <input v-model.number="draft.pageSize" type="number" min="10" max="5000" step="10" />
            <span class="field-hint">10 – 5000，同时同步为查询的默认 max_rows</span>
          </div>
          <div class="field">
            <label class="field-label">危险操作</label>
            <label class="check-line" style="padding-top: 6px">
              <input v-model="draft.confirmDangerous" type="checkbox" /> 删除 / 截断前二次确认
            </label>
            <span class="field-hint">关闭后危险操作将直接执行，请谨慎使用</span>
          </div>
        </div>
      </section>

      <div class="alert alert-info">
        <Icon name="info" :size="14" />
        <span class="grow">设置保存在本机，修改时即时预览，保存后立即生效；分页大小、主题与确认策略会同步到应用配置。</span>
      </div>
    </div>

    <template #footer>
      <button class="btn" @click="restoreDefaults"><Icon name="refresh" :size="13" /> 恢复默认</button>
      <span class="grow"></span>
      <button class="btn" @click="cancel">取消</button>
      <button class="btn btn-primary" @click="save"><Icon name="save" :size="13" /> 保存设置</button>
    </template>
  </Modal>
</template>

<style scoped>
.theme-row { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
.theme-card {
  display: flex; align-items: center; gap: 9px; position: relative;
  padding: 9px; text-align: left; border: 1px solid var(--border);
  border-radius: var(--radius); background: var(--surface-2); color: var(--text); cursor: pointer;
}
.theme-card:hover { border-color: var(--border-strong); }
.theme-card.active { border-color: var(--accent); background: var(--accent-soft); }
.theme-card strong { font-size: var(--fs-sm); }
.theme-card small { font-size: var(--fs-xxs); }
.swatch { width: 22px; height: 22px; border-radius: 6px; flex-shrink: 0; border: 1px solid var(--border-strong); }
.sw-dark { background: linear-gradient(135deg, #171a24 50%, #7c6cff 50%); }
.sw-light { background: linear-gradient(135deg, #ffffff 50%, #5b4bd6 50%); }
.sw-midnight { background: linear-gradient(135deg, #0e1424 50%, #4dd0e1 50%); }
.theme-check { color: var(--accent-hover); flex-shrink: 0; }
input[type="range"] { padding: 0; border: 0; background: transparent; accent-color: var(--accent); cursor: pointer; width: 100%; }
input[type="range"]:focus { box-shadow: none; }
section { min-width: 0; }
</style>
