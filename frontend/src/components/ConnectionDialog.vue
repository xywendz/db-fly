<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useWorkspace } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ConnectionConfig, Kind, KindInfo } from "@/types/domain";
import { KIND_FORMS, KIND_ORDER, TLS_OPTIONS, blankFor, formOf } from "@/config/kinds";
import Modal from "./ui/Modal.vue";
import Icon from "./ui/Icon.vue";

const props = defineProps<{ open: boolean; editing: ConnectionConfig | null }>();
const emit = defineEmits<{ (e: "close"): void; (e: "saved"): void }>();
const store = useWorkspace();

const kinds = ref<KindInfo[]>([]);
const step = ref<1 | 2>(1);
const form = ref<ConnectionConfig>(blankFor("mysql"));
const password = ref("");
const testing = ref(false);
const saving = ref(false);
const testState = ref<{ level: "ok" | "error" | "info"; text: string } | null>(null);
const errors = ref<Record<string, string>>({});
const showAdvanced = ref(false);
const extraParams = ref<{ key: string; value: string }[]>([]);

const cfg = computed(() => formOf(form.value.kind));
const isEdit = computed(() => !!props.editing?.name);
const kindInfo = computed(() => kinds.value.find((k) => k.kind === form.value.kind));
const availableKinds = computed<Kind[]>(() =>
  kinds.value.length
    ? kinds.value.map((k) => k.kind as Kind).filter((k) => KIND_ORDER.includes(k))
    : KIND_ORDER,
);

async function ensureKinds() {
  if (kinds.value.length) return;
  try {
    kinds.value = await api.listKinds();
  } catch {
    kinds.value = [];
  }
}

function syncExtraParams() {
  const params: Record<string, string> = { ...(form.value.params ?? {}) };
  for (const row of extraParams.value) {
    const key = row.key.trim();
    if (!key) continue;
    if (row.value === "") delete params[key];
    else params[key] = row.value;
  }
  form.value.params = params;
}

function loadGenericParams() {
  const fixed = new Set(cfg.value.extras.map((f) => f.key));
  extraParams.value = Object.entries(form.value.params ?? {})
    .filter(([k]) => !fixed.has(k))
    .map(([key, value]) => ({ key, value }));
}

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    await ensureKinds();
    step.value = props.editing ? 2 : 1;
    if (props.editing) {
      form.value = { ...props.editing, params: { ...(props.editing.params ?? {}) } };
    } else {
      form.value = blankFor("mysql");
      const k = kinds.value.find((x) => x.kind === form.value.kind);
      if (k) form.value.port = k.default_port;
    }
    password.value = "";
    testState.value = null;
    errors.value = {};
    showAdvanced.value = false;
    loadGenericParams();
  },
);

function chooseKind(kind: Kind) {
  const name = form.value.name;
  const next = blankFor(kind);
  const info = kinds.value.find((k) => k.kind === kind);
  if (info?.default_port) next.port = info.default_port;
  next.name = name;
  form.value = next;
  password.value = "";
  testState.value = null;
  errors.value = {};
  extraParams.value = [];
  step.value = 2;
}

function getParam(key: string) {
  return form.value.params?.[key] ?? "";
}
function setParam(key: string, value: string) {
  if (!form.value.params) form.value.params = {};
  if (value === "") delete form.value.params[key];
  else form.value.params[key] = value;
}
function onParamInput(key: string, e: Event) {
  setParam(key, (e.target as HTMLInputElement).value);
}

function validate(): boolean {
  const e: Record<string, string> = {};
  if (!form.value.name?.trim()) e.name = "请填写连接名称";
  if (cfg.value.fileBased) {
    if (!form.value.host?.trim() && !getParam("file")) e.host = "请填写数据库文件路径";
  } else {
    if (!form.value.host?.trim()) e.host = "请填写主机地址";
    if (!form.value.port || form.value.port < 1 || form.value.port > 65535) {
      e.port = "端口需在 1–65535 之间";
    }
  }
  if (form.value.kind === "oracle" && !form.value.database?.trim() && !getParam("service") && !getParam("SID")) {
    e.database = "请填写 Oracle 服务名 / SID";
  }
  errors.value = e;
  return Object.keys(e).length === 0;
}

async function test() {
  syncExtraParams();
  if (!validate()) return;
  testing.value = true;
  testState.value = { level: "info", text: "正在测试连接…" };
  try {
    const info = await api.testConnection(form.value, password.value);
    testState.value = { level: "ok", text: `连接成功 · ${info.kind} ${info.version}${info.host ? " · " + info.host : ""}` };
  } catch (e: any) {
    testState.value = { level: "error", text: `连接失败：${e?.message ?? e}` };
  } finally {
    testing.value = false;
  }
}

async function save() {
  syncExtraParams();
  if (!validate()) return;
  saving.value = true;
  try {
    await api.saveConnection(form.value, password.value, password.value !== "");
    store.setStatus(isEdit.value ? `已更新连接 ${form.value.name}` : `已创建连接 ${form.value.name}`, "ok");
    emit("saved");
    emit("close");
  } catch (e: any) {
    testState.value = { level: "error", text: `保存失败：${e?.message ?? e}` };
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Modal
    :open="open"
    :title="isEdit ? `编辑连接 · ${editing?.name}` : '新建数据库连接'"
    :subtitle="step === 1 ? '第 1 步：选择数据库类型' : `第 2 步：配置 ${cfg.label} 专属参数`"
    :width="step === 1 ? '680px' : '560px'"
    :close-on-mask="false"
    @close="emit('close')"
  >
    <!-- 第 1 步：按类型选择，不同数据库进入不同表单 -->
    <div v-if="step === 1" class="kind-grid">
      <button
        v-for="k in availableKinds"
        :key="k"
        class="kind-card"
        @click="chooseKind(k)"
      >
        <span class="kind-top">
          <span class="kind-icon"><Icon :name="KIND_FORMS[k].icon" :size="19" /></span>
          <span class="badge" :class="KIND_FORMS[k].badge">{{ k }}</span>
        </span>
        <strong>{{ KIND_FORMS[k].label }}</strong>
        <small>{{ KIND_FORMS[k].summary }}</small>
        <span class="kind-tips">{{ KIND_FORMS[k].tips[0] }}</span>
      </button>
    </div>

    <!-- 第 2 步：类型专属字段 -->
    <div v-else class="col" style="gap: 14px">
      <div class="kind-current">
        <span class="kind-icon"><Icon :name="cfg.icon" :size="18" /></span>
        <span class="col" style="gap: 2px">
          <strong>{{ cfg.label }}</strong>
          <small class="faint">{{ cfg.summary }}</small>
        </span>
        <span class="grow"></span>
        <button class="btn btn-sm" @click="step = 1">更换类型</button>
      </div>

      <div class="form-grid">
        <div class="field span-2">
          <label class="field-label">连接名称 *</label>
          <input v-model="form.name" placeholder="例如：本地 MySQL" :class="{ invalid: errors.name }" />
          <span v-if="errors.name" class="field-error">{{ errors.name }}</span>
        </div>
        <div class="field span-2">
          <label class="field-label">{{ cfg.hostLabel }}</label>
          <input
            v-model="form.host"
            :placeholder="cfg.hostPlaceholder"
            :class="{ invalid: errors.host }"
          />
          <span v-if="errors.host" class="field-error">{{ errors.host }}</span>
          <span v-if="cfg.fileBased" class="field-hint">也可填 :memory: 使用内存数据库。文件不存在时会自动创建。</span>
        </div>
        <div v-if="cfg.showPort" class="field">
          <label class="field-label">端口 *</label>
          <input v-model.number="form.port" type="number" min="1" max="65535" :class="{ invalid: errors.port }" />
          <span v-if="errors.port" class="field-error">{{ errors.port }}</span>
        </div>
        <div v-if="cfg.showDatabase" class="field">
          <label class="field-label">{{ cfg.databaseLabel }}</label>
          <input v-model="form.database" :placeholder="cfg.databasePlaceholder" :class="{ invalid: errors.database }" />
          <span v-if="errors.database" class="field-error">{{ errors.database }}</span>
        </div>
        <div v-if="cfg.showUser" class="field">
          <label class="field-label">用户名</label>
          <input v-model="form.user" placeholder="例如 root / postgres / sa" />
        </div>
        <div v-if="cfg.showPassword" class="field">
          <label class="field-label">密码{{ form.kind === "redis" ? "（Redis 只使用密码）" : "" }}</label>
          <input v-model="password" type="password" :placeholder="isEdit ? '留空表示不修改' : '连接密码'" />
          <span v-if="isEdit" class="field-hint">留空则保留原有凭证</span>
        </div>
      </div>

      <div v-if="cfg.extras.length" class="card">
        <div class="card-head"><Icon name="sliders" :size="13" /><h4>{{ cfg.label }} 专属参数</h4></div>
        <div class="card-body form-grid">
          <div v-for="f in cfg.extras" :key="f.key" class="field">
            <label class="field-label">{{ f.label }}</label>
            <input :value="getParam(f.key)" :placeholder="f.placeholder || ''" @input="onParamInput(f.key, $event)" />
          </div>
        </div>
      </div>

      <div class="tips-box">
        <Icon name="info" :size="13" />
        <span class="row wrap" style="gap: 5px">
          <span v-for="(tip, i) in cfg.tips" :key="i" class="badge">{{ tip }}</span>
        </span>
      </div>

      <button class="btn btn-sm" style="align-self: flex-start" @click="showAdvanced = !showAdvanced">
        <Icon :name="showAdvanced ? 'chevron-down' : 'chevron-right'" :size="13" /> 高级选项
      </button>

      <div v-if="showAdvanced" class="card">
        <div class="card-body form-grid">
          <div class="field"><label class="field-label">分组</label><input v-model="form.group" placeholder="例如：生产环境" /></div>
          <div class="field"><label class="field-label">标识色</label><input v-model="form.color" placeholder="#7c6cff" /></div>
          <div v-if="cfg.showTLS" class="field">
            <label class="field-label">TLS / SSL 模式</label>
            <select v-model="form.tls_mode">
              <option v-for="o in TLS_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </div>
          <div class="field"><label class="field-label">超时（秒）</label><input v-model.number="form.timeout_sec" type="number" min="0" /></div>
          <label class="check-line span-2"><input type="checkbox" v-model="form.read_only" /> 只读连接（禁止任何回写与结构变更）</label>
          <div class="span-2 col" style="gap: 6px">
            <div class="row">
              <span class="dim" style="font-size: 11px">附加连接参数（原样传给驱动 DSN / URI）</span>
              <span class="grow"></span>
              <button class="btn btn-sm" @click="extraParams.push({ key: '', value: '' })"><Icon name="plus" :size="12" /> 添加</button>
            </div>
            <div v-for="(p, i) in extraParams" :key="i" class="row" style="gap: 6px">
              <input v-model="p.key" class="mono" placeholder="参数名" style="width: 150px" />
              <input v-model="p.value" class="grow mono" placeholder="参数值" />
              <button class="btn btn-icon" title="移除" @click="extraParams.splice(i, 1)"><Icon name="close" :size="12" /></button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="kindInfo?.capabilities?.length" class="row wrap" style="gap: 4px">
        <span class="faint" style="font-size: 11px">支持能力：</span>
        <span v-for="cap in kindInfo.capabilities.slice(0, 10)" :key="cap" class="badge">{{ cap }}</span>
      </div>

      <div v-if="testState" class="alert" :class="`alert-${testState.level}`">
        <Icon :name="testState.level === 'ok' ? 'check' : testState.level === 'error' ? 'alert' : 'info'" :size="14" />
        <span class="grow">{{ testState.text }}</span>
      </div>
    </div>

    <template #footer>
      <template v-if="step === 2">
        <button class="btn" :disabled="testing || saving" @click="test">
          <span v-if="testing" class="spinner" style="width: 12px; height: 12px"></span>
          <Icon v-else name="bolt" :size="13" /> 测试连接
        </button>
      </template>
      <span class="grow"></span>
      <button class="btn" @click="emit('close')">取消</button>
      <button v-if="step === 2" class="btn btn-primary" :disabled="saving" @click="save">
        <Icon name="save" :size="13" /> 保存
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.invalid { border-color: var(--danger); }
.kind-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.kind-card { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; text-align: left; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface-2); color: var(--text); cursor: pointer; min-width: 0; }
.kind-card:hover { border-color: var(--accent); background: var(--accent-soft); }
.kind-card strong { font-size: 13px; }
.kind-card small { color: var(--text-dim); font-size: 11px; line-height: 1.5; }
.kind-top { display: flex; align-items: center; justify-content: space-between; width: 100%; }
.kind-icon { display: inline-flex; align-items: center; justify-content: center; width: 32px; height: 32px; border-radius: 9px; background: var(--surface-3); color: var(--accent-hover); flex-shrink: 0; }
.kind-tips { font-size: 10px; color: var(--text-faint); font-family: var(--mono); }
.kind-current { display: flex; align-items: center; gap: 10px; padding: 9px 11px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface-2); }
.kind-current small { font-size: 11px; }
.tips-box { display: flex; align-items: flex-start; gap: 8px; color: var(--text-dim); }
</style>
