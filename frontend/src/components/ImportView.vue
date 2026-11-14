<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useWorkspace, type Tab } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ImportOutcomeDTO } from "@/types/domain";
import Icon from "./ui/Icon.vue";
import EmptyState from "./ui/EmptyState.vue";
import { pickImportFile, runtimeMode } from "@/runtime";

const props = defineProps<{ tab: Tab }>();
const store = useWorkspace();

const STEPS = ["选择目标", "选择文件", "导入选项", "执行结果"];
const FORMATS = [
  { value: "csv", label: "CSV（逗号分隔）" },
  { value: "tsv", label: "TSV（制表符分隔）" },
  { value: "sql", label: "SQL 脚本" },
  { value: "xlsx", label: "Excel (XLSX)" },
  { value: "json", label: "JSON / NDJSON" },
];

const step = ref(1);
const connName = ref(props.tab.conn);
const dbName = ref(props.tab.database ?? "");
const tableName = ref(props.tab.table ?? "");
const path = ref("");
const pickedName = ref("");
const format = ref(props.tab.format || "csv");
const sheet = ref("");
// 连接类型感知：关系型支持全部格式；文档/键值库没有 SQL 概念，导入不提供 SQL 脚本。
const kind = computed(() => store.connections.find((c) => c.name === connName.value)?.kind ?? "");
const isES = computed(() => kind.value === "elasticsearch");
const isRelational = computed(() => !["mongodb", "redis", "elasticsearch"].includes(kind.value));
const importFormats = computed(() => (isRelational.value ? FORMATS : FORMATS.filter((f) => f.value !== "sql")));
// 切换到非关系型连接时，若之前选了 SQL 格式则回退到 CSV，避免无效选择。
watch(connName, () => { if (!isRelational.value && format.value === "sql") format.value = "csv"; });
const hasHeader = ref(true);
const batchSize = ref(500);
const transaction = ref(true);
const dryRun = ref(true);
const busy = ref(false);
const result = ref<ImportOutcomeDTO | null>(null);
const error = ref("");

const databases = computed(() => store.dbCache[connName.value] || []);
const tables = computed(() =>
  dbName.value ? store.objectsCache[store.cacheKey(connName.value, dbName.value)] || [] : [],
);
const needHeader = computed(() => format.value === "csv" || format.value === "tsv" || format.value === "xlsx");
const formatLabel = computed(() => FORMATS.find((f) => f.value === format.value)?.label ?? format.value);

// GUI 模式走 Wails 原生文件对话框；Web 模式走浏览器选文件 + /api/upload 上传。
async function pickFile() {
  error.value = "";
  try {
    const picked = await pickImportFile();
    if (!picked) return;
    path.value = picked.path;
    pickedName.value = picked.name;
    const lower = picked.name.toLowerCase();
    const detected =
      lower.endsWith(".tsv") ? "tsv" :
      lower.endsWith(".sql") ? "sql" :
      lower.endsWith(".xlsx") || lower.endsWith(".xlsm") ? "xlsx" :
      lower.endsWith(".json") || lower.endsWith(".ndjson") ? "json" :
      lower.endsWith(".csv") ? "csv" : "";
    if (detected) format.value = detected;
  } catch (e: any) {
    error.value = e?.message ?? String(e);
  }
}

function next() {
  error.value = "";
  if (step.value === 1) {
    if (!connName.value) { error.value = "请选择目标连接"; return; }
    // SQL 脚本可自带建库 / 建表语句，目标库与表可留空；数据文件仍需指定已存在的目标表。
    if (format.value !== "sql") {
      if (!dbName.value.trim()) { error.value = "请填写目标数据库"; return; }
      if (!tableName.value.trim()) { error.value = "请填写目标表"; return; }
    }
  } else if (step.value === 2) {
    if (!path.value.trim()) { error.value = "请先选择要导入的文件"; return; }
  }
  step.value = Math.min(4, step.value + 1);
}
function prev() { error.value = ""; step.value = Math.max(1, step.value - 1); }
function goto(n: number) { if (n <= step.value) { step.value = n; error.value = ""; } }

// dry=true 进入「预览」模式（只生成预览，不写库）；dry=false 执行「导入数据」（真实写入）。
async function run(dry: boolean) {
  error.value = "";
  if (!path.value.trim()) { error.value = "请先选择要导入的文件"; step.value = 2; return; }
  dryRun.value = dry;
  busy.value = true;
  result.value = null;
  step.value = 4;
  try {
    result.value = await api.importFile({
      conn: connName.value,
      ref: { database: dbName.value.trim(), name: tableName.value.trim() },
      path: path.value.trim(),
      format: format.value,
      sheet: sheet.value || undefined,
      has_header: hasHeader.value,
      batch_size: batchSize.value,
      transaction: transaction.value,
      dry_run: dry,
    });
    store.setStatus(dry ? "导入预览完成" : `导入完成 · ${result.value.applied} 行`, "ok");
  } catch (e: any) {
    error.value = e?.message ?? String(e);
    store.setStatus(`导入失败：${error.value}`, "error");
  } finally {
    busy.value = false;
  }
}

function restart() {
  result.value = null;
  error.value = "";
  path.value = "";
  pickedName.value = "";
  dryRun.value = true;
  step.value = 1;
}
</script>

<template>
  <div class="import-page">
    <div class="toolbar page-head">
      <Icon name="upload" :size="15" />
      <strong>导入数据</strong>
      <span class="badge">{{ connName }}</span>
      <span v-if="dbName" class="badge">{{ dbName }}</span>
      <span v-if="tableName" class="badge badge-accent">{{ tableName }}</span>
      <span class="grow"></span>
      <span class="faint" style="font-size: 11px">按步骤配置，先预览再写入</span>
    </div>

    <div class="stepper">
      <button
        v-for="(s, i) in STEPS"
        :key="s"
        class="step"
        :class="{ active: step === i + 1, done: step > i + 1 }"
        :disabled="i + 1 > step"
        @click="goto(i + 1)"
      >
        <span class="step-no">{{ step > i + 1 ? "✓" : i + 1 }}</span>
        <span>{{ s }}</span>
      </button>
    </div>

    <div class="page-body scroll">
      <template v-if="step === 1">
        <div class="card">
          <div class="card-head"><Icon name="database" :size="14" /><h4>导入目标</h4></div>
          <div class="card-body form-grid">
            <div class="field">
              <label class="field-label">目标连接 *</label>
              <select v-model="connName"><option v-for="c in store.connections" :key="c.name" :value="c.name">{{ c.name }}</option></select>
            </div>
            <div class="field">
              <label class="field-label">文件格式</label>
              <select v-model="format"><option v-for="f in importFormats" :key="f.value" :value="f.value">{{ f.label }}</option></select>
            </div>
            <div class="field">
              <label class="field-label">目标数据库<span v-if="format !== 'sql'" class="req"> *</span></label>
              <input v-model="dbName" list="import-db" placeholder="数据库名（SQL 可留空）" />
              <datalist id="import-db"><option v-for="d in databases" :key="d.name" :value="d.name"></option></datalist>
            </div>
            <div class="field">
              <label class="field-label">{{ isRelational ? '目标表' : isES ? '目标索引' : '目标集合' }}<span v-if="format !== 'sql'" class="req"> *</span></label>
              <select v-model="tableName">
                <option value="">{{ dbName ? `选择${isRelational ? '表' : isES ? '索引' : '集合'}…` : '请先选择数据库' }}</option>
                <option v-for="t in tables" :key="t.name" :value="t.name">{{ t.name }}</option>
              </select>
            </div>
          </div>
        </div>
        <div v-if="isES && format !== 'sql'" class="alert alert-info">
          <Icon name="info" :size="14" />
          <span>
            Elasticsearch 每批数据通过 <code>_bulk</code> NDJSON 一次写入；JSON / NDJSON 文件每行一个文档，
            可带 <code>_id</code> 指定文档 ID（不会写入 _source）。先「预览」核对将要发送的 bulk 内容再导入。
          </span>
        </div>
        <div class="alert alert-info">
          <Icon name="info" :size="14" />
          <span v-if="format === 'sql'">
            SQL 脚本可自带建库 / 建表语句（如 <code>CREATE DATABASE</code>、<code>CREATE TABLE</code>），因此目标库与表均可留空。
            若未选择数据库且脚本中也没有建库 / 建表语句，导入会报错提示。
          </span>
          <span v-else>
            数据文件（CSV / TSV / Excel / JSON）需写入<strong>已存在</strong>的目标表，请填写目标数据库与表名。
          </span>
        </div>
      </template>

      <template v-else-if="step === 2">
        <div class="card">
          <div class="card-head"><Icon name="file" :size="14" /><h4>选择要导入的文件 · {{ formatLabel }}</h4></div>
          <div class="card-body col" style="gap: 10px">
            <button type="button" class="file-picker" @click="pickFile">
              <Icon name="upload" :size="20" />
              <span class="col" style="gap: 2px">
                <strong>{{ pickedName || "点击选择本地文件" }}</strong>
                <small class="faint">支持 CSV / TSV / SQL / XLSX / JSON，选择后按扩展名自动识别格式</small>
              </span>
            </button>
            <div class="field">
              <label class="field-label">{{ runtimeMode === "web" ? "服务端临时文件路径 *" : "文件绝对路径 *" }}</label>
              <div class="row" style="gap: 6px">
                <Icon name="file" :size="15" class="dim" />
                <input
                  v-model="path"
                  class="grow mono"
                  :readonly="runtimeMode === 'web'"
                  :placeholder="runtimeMode === 'web' ? '请点击上方按钮上传文件' : 'D:\\data\\users.csv'"
                />
              </div>
              <span class="field-hint">{{ runtimeMode === "web"
                ? "文件已上传到服务端临时目录，重新选择即可替换；关闭服务后自动清理。"
                : "已通过系统文件对话框选择，路径会自动填入；如需也可手动粘贴完整路径。" }}</span>
            </div>
          </div>
        </div>
      </template>

      <template v-else-if="step === 3">
        <div class="card">
          <div class="card-head"><Icon name="settings" :size="14" /><h4>导入选项</h4></div>
          <div class="card-body form-grid">
            <div v-if="format === 'xlsx'" class="field">
              <label class="field-label">工作表名</label>
              <input v-model="sheet" placeholder="留空使用第一个工作表" />
            </div>
            <div class="field">
              <label class="field-label">每批行数</label>
              <input v-model.number="batchSize" type="number" min="1" max="10000" />
            </div>
            <div class="col span-2" style="gap: 8px">
              <label v-if="needHeader" class="check-line"><input type="checkbox" v-model="hasHeader" /> 首行为表头（列名）</label>
              <label v-if="!isES" class="check-line"><input type="checkbox" v-model="transaction" /> 使用事务（任一批失败则整体回滚）</label>
              <span v-if="isES" class="field-hint">
                Elasticsearch 没有跨文档事务：每批一次 <code>_bulk</code>，逐条动作各自原子；失败时会在结果中列出具体错误。
              </span>
            </div>
          </div>
        </div>
      </template>

      <template v-else>
        <div v-if="busy" class="empty"><span class="spinner" style="width: 22px; height: 22px"></span><p>正在解析并导入…</p></div>
        <template v-else>
          <EmptyState
            v-if="!result"
            :compact="true"
            icon="upload"
            title="准备就绪"
            description="点击「预览」核对将要写入的内容，确认无误后点「导入数据」真实写入。"
          />
          <div v-else class="card">
            <div class="card-head"><Icon name="check" :size="14" /><h4>{{ dryRun ? "预览结果" : "导入结果" }}</h4></div>
            <div class="card-body col" style="gap: 10px">
              <div class="row wrap" style="gap: 6px">
                <span class="badge badge-info">格式 {{ result.format }}</span>
                <span class="badge">解析 {{ result.rows }} 行</span>
                <span class="badge">生成 {{ result.statements }} 条语句</span>
                <span class="badge" :class="result.applied ? 'badge-ok' : ''">已应用 {{ result.applied }}</span>
              </div>
              <pre v-if="result.preview?.length" class="code result-preview">{{ result.preview.join("\n") }}</pre>
              <div class="row" style="gap: 6px">
                <span class="grow"></span>
                <button class="btn btn-sm" @click="restart"><Icon name="refresh" :size="12" /> 再导入一次</button>
              </div>
            </div>
          </div>
        </template>
      </template>

      <div v-if="error" class="alert alert-error"><Icon name="alert" :size="14" /><span class="grow">{{ error }}</span></div>
    </div>

    <div class="toolbar page-foot">
      <button class="btn" :disabled="step === 1" @click="prev"><Icon name="arrow-left" :size="13" /> 上一步</button>
      <button v-if="step < 3" class="btn btn-primary" @click="next">下一步 <Icon name="arrow-right" :size="13" /></button>
      <template v-else>
        <span class="grow"></span>
        <button class="btn" :disabled="busy" @click="goto(3)"><Icon name="settings" :size="13" /> 修改选项</button>
        <button class="btn" :disabled="busy" @click="run(true)"><Icon name="eye" :size="13" /> 预览</button>
        <button class="btn btn-primary" :disabled="busy" @click="run(false)">
          <span v-if="busy" class="spinner" style="width: 12px; height: 12px"></span>
          <Icon v-else name="upload" :size="13" /> 导入数据
        </button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.import-page { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; width: 100%; overflow: hidden; }
.page-head, .page-foot { flex-shrink: 0; }
.stepper { display: flex; gap: 4px; padding: 8px 10px; background: var(--surface); border-bottom: 1px solid var(--border); flex-shrink: 0; flex-wrap: wrap; }
.step { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border: 1px solid transparent; border-radius: 20px; background: transparent; color: var(--text-dim); font-size: 11px; cursor: pointer; }
.step:disabled { cursor: default; opacity: .6; }
.step.active { background: var(--accent-soft); border-color: var(--accent); color: var(--accent-hover); }
.step.done { color: var(--ok); }
.step-no { display: inline-flex; align-items: center; justify-content: center; width: 17px; height: 17px; border-radius: 50%; background: var(--surface-3); font-size: 10px; }
.step.active .step-no { background: var(--accent); color: #fff; }
.page-body { flex: 1; min-height: 0; display: flex; flex-direction: column; gap: 10px; padding: 10px; background: var(--bg); }
.page-body > .card { flex-shrink: 0; }
.file-picker { display: flex; align-items: center; gap: 12px; width: 100%; padding: 16px; border: 1px dashed var(--border-strong); border-radius: var(--radius); background: var(--surface-2); cursor: pointer; font: inherit; color: inherit; text-align: left; }
.file-picker:hover { border-color: var(--accent); }
.file-picker:hover { border-color: var(--accent); }
.hidden-file { display: none; }
.result-preview { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 9px; font-size: 11px; line-height: 1.6; max-height: 220px; overflow: auto; }
.req { color: var(--danger); margin-left: 2px; }
.alert code { font-family: var(--mono); font-size: 10px; background: var(--surface-3); padding: 1px 4px; border-radius: 3px; }
</style>
