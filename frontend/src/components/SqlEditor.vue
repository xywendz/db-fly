<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useWorkspace } from "@/stores/workspace";
import * as api from "@/api/client";
import type { ColumnInfo } from "@/types/domain";

const props = defineProps<{
  modelValue: string;
  language: string;
  conn: string;
  database: string;
  height: number;
  placeholder: string;
}>();
const emit = defineEmits<{
  (e: "update:modelValue", value: string): void;
  (e: "run"): void;
}>();

const store = useWorkspace();
const ta = ref<HTMLTextAreaElement | null>(null);
const layer = ref<HTMLPreElement | null>(null);
const wrap = ref<HTMLDivElement | null>(null);

type TokenClass = "tok-kw" | "tok-fn" | "tok-str" | "tok-num" | "tok-comment" | "tok-ident" | "tok-table" | "tok-col" | "";
interface Token { text: string; cls: TokenClass; title?: string }
type SuggestKind = "keyword" | "table" | "column" | "database";
interface Suggestion { label: string; insert: string; kind: SuggestKind; detail: string }
interface TableMeta {
  name: string;
  schema?: string;
  database: string;
  type: string;
  comment?: string;
  rows?: number;
}

const KEYWORDS: { word: string; desc: string }[] = [
  { word: "SELECT", desc: "查询数据" }, { word: "FROM", desc: "数据源" },
  { word: "WHERE", desc: "过滤条件" }, { word: "GROUP BY", desc: "分组" },
  { word: "HAVING", desc: "分组后过滤" }, { word: "ORDER BY", desc: "排序" },
  { word: "LIMIT", desc: "限制行数" }, { word: "OFFSET", desc: "偏移量" },
  { word: "FETCH", desc: "取数" }, { word: "TOP", desc: "取前 N 行" },
  { word: "AS", desc: "别名" }, { word: "DISTINCT", desc: "去重" },
  { word: "JOIN", desc: "连接" }, { word: "INNER JOIN", desc: "内连接" },
  { word: "LEFT JOIN", desc: "左连接" }, { word: "RIGHT JOIN", desc: "右连接" },
  { word: "FULL JOIN", desc: "全连接" }, { word: "CROSS JOIN", desc: "交叉连接" },
  { word: "ON", desc: "连接条件" }, { word: "USING", desc: "连接列" },
  { word: "UNION", desc: "合并结果" }, { word: "UNION ALL", desc: "合并结果（保留重复）" },
  { word: "WITH", desc: "公共表表达式" }, { word: "RECURSIVE", desc: "递归查询" },
  { word: "INSERT INTO", desc: "插入数据" }, { word: "VALUES", desc: "值列表" },
  { word: "UPDATE", desc: "更新数据" }, { word: "SET", desc: "赋值" },
  { word: "DELETE", desc: "删除数据" }, { word: "MERGE", desc: "合并写入" },
  { word: "UPSERT", desc: "存在则更新" }, { word: "RETURNING", desc: "返回变更行" },
  { word: "CREATE", desc: "创建" }, { word: "ALTER", desc: "修改结构" },
  { word: "DROP", desc: "删除对象" }, { word: "TRUNCATE", desc: "清空表" },
  { word: "TABLE", desc: "表" }, { word: "VIEW", desc: "视图" },
  { word: "INDEX", desc: "索引" }, { word: "SCHEMA", desc: "模式" },
  { word: "DATABASE", desc: "数据库" }, { word: "PRIMARY KEY", desc: "主键" },
  { word: "FOREIGN KEY", desc: "外键" }, { word: "REFERENCES", desc: "外键引用" },
  { word: "UNIQUE", desc: "唯一约束" }, { word: "CHECK", desc: "检查约束" },
  { word: "CONSTRAINT", desc: "约束" }, { word: "DEFAULT", desc: "默认值" },
  { word: "AUTO_INCREMENT", desc: "自增" }, { word: "NULL", desc: "空值" },
  { word: "NOT NULL", desc: "非空" }, { word: "IS", desc: "判断" },
  { word: "IN", desc: "包含于" }, { word: "LIKE", desc: "模糊匹配" },
  { word: "BETWEEN", desc: "区间" }, { word: "EXISTS", desc: "存在" },
  { word: "AND", desc: "与" }, { word: "OR", desc: "或" },
  { word: "NOT", desc: "非" }, { word: "CASE", desc: "条件分支" },
  { word: "WHEN", desc: "分支条件" }, { word: "THEN", desc: "分支结果" },
  { word: "ELSE", desc: "其他结果" }, { word: "END", desc: "结束" },
  { word: "CAST", desc: "类型转换" }, { word: "INTERVAL", desc: "时间间隔" },
  { word: "OVER", desc: "窗口函数" }, { word: "PARTITION BY", desc: "窗口分区" },
  { word: "ROWS", desc: "窗口行" }, { word: "RANGE", desc: "窗口范围" },
  { word: "EXPLAIN", desc: "执行计划" }, { word: "ANALYZE", desc: "分析" },
  { word: "SHOW", desc: "查看" }, { word: "DESCRIBE", desc: "查看结构" },
  { word: "USE", desc: "切换数据库" }, { word: "BEGIN", desc: "开始事务" },
  { word: "COMMIT", desc: "提交事务" }, { word: "ROLLBACK", desc: "回滚事务" },
  { word: "TRUE", desc: "真" }, { word: "FALSE", desc: "假" },
];
const KEYWORD_WORDS = new Map<string, string>();
for (const k of KEYWORDS) {
  for (const w of k.word.split(" ")) KEYWORD_WORDS.set(w, k.desc);
}
const FUNCTIONS = new Set([
  "COUNT", "SUM", "AVG", "MIN", "MAX", "COALESCE", "IFNULL", "NULLIF",
  "CONCAT", "SUBSTRING", "SUBSTR", "LENGTH", "UPPER", "LOWER", "TRIM",
  "ROUND", "ABS", "CEIL", "FLOOR", "NOW", "CURRENT_DATE", "CURRENT_TIMESTAMP",
  "DATE", "DATEDIFF", "DATE_FORMAT", "EXTRACT", "ROW_NUMBER", "RANK",
  "DENSE_RANK", "LAG", "LEAD", "JSON_EXTRACT", "JSON_VALUE", "GROUP_CONCAT",
]);
const ALIAS_STOP = new Set([
  "LEFT", "RIGHT", "INNER", "OUTER", "FULL", "CROSS", "ON", "WHERE", "GROUP",
  "ORDER", "HAVING", "LIMIT", "OFFSET", "UNION", "SET", "VALUES", "WITH",
  "USING", "JOIN", "RETURNING", "FOR", "AS", "AND", "OR",
]);

const MONGO_WORDS: { word: string; desc: string }[] = [
  { word: "collection", desc: "集合名" }, { word: "filter", desc: "过滤条件" },
  { word: "sort", desc: "排序" }, { word: "projection", desc: "投影字段" },
  { word: "skip", desc: "跳过条数" }, { word: "limit", desc: "限制条数" },
  { word: "pipeline", desc: "聚合管道" },
  { word: "$eq", desc: "等于" }, { word: "$ne", desc: "不等于" },
  { word: "$gt", desc: "大于" }, { word: "$gte", desc: "大于等于" },
  { word: "$lt", desc: "小于" }, { word: "$lte", desc: "小于等于" },
  { word: "$in", desc: "包含于列表" }, { word: "$nin", desc: "不包含于列表" },
  { word: "$and", desc: "并且" }, { word: "$or", desc: "或者" },
  { word: "$nor", desc: "都不满足" }, { word: "$not", desc: "取反" },
  { word: "$exists", desc: "字段存在" }, { word: "$type", desc: "字段类型" },
  { word: "$regex", desc: "正则匹配" }, { word: "$options", desc: "正则选项" },
  { word: "$all", desc: "包含所有元素" }, { word: "$elemMatch", desc: "数组元素匹配" },
  { word: "$size", desc: "数组长度" }, { word: "$expr", desc: "表达式" },
  { word: "$text", desc: "全文搜索" }, { word: "$mod", desc: "取模匹配" },
  { word: "$match", desc: "管道：筛选文档" }, { word: "$group", desc: "管道：分组" },
  { word: "$sort", desc: "管道：排序" }, { word: "$project", desc: "管道：投影" },
  { word: "$limit", desc: "管道：限制条数" }, { word: "$skip", desc: "管道：跳过条数" },
  { word: "$count", desc: "管道：计数" }, { word: "$unwind", desc: "管道：展开数组" },
  { word: "$lookup", desc: "管道：关联集合" }, { word: "$addFields", desc: "管道：新增字段" },
  { word: "$facet", desc: "管道：多面聚合" }, { word: "$sortByCount", desc: "管道：按数量排序" },
  { word: "$replaceRoot", desc: "管道：替换根文档" }, { word: "$unset", desc: "管道：移除字段" },
];
const ES_WORDS: { word: string; desc: string }[] = [
  { word: "index", desc: "索引名" }, { word: "query", desc: "查询条件" },
  { word: "size", desc: "返回条数" }, { word: "from", desc: "起始位置" },
  { word: "sort", desc: "排序" }, { word: "_source", desc: "返回字段" },
  { word: "aggs", desc: "聚合" }, { word: "aggregations", desc: "聚合" },
  { word: "highlight", desc: "高亮" }, { word: "post_filter", desc: "后置过滤" },
  { word: "track_total_hits", desc: "精确总数" }, { word: "match_all", desc: "匹配全部" },
  { word: "match", desc: "全文匹配" }, { word: "match_phrase", desc: "短语匹配" },
  { word: "multi_match", desc: "多字段匹配" }, { word: "term", desc: "精确匹配" },
  { word: "terms", desc: "多值精确匹配" }, { word: "range", desc: "范围查询" },
  { word: "bool", desc: "布尔组合" }, { word: "must", desc: "必须满足" },
  { word: "should", desc: "应当满足" }, { word: "must_not", desc: "必须不满足" },
  { word: "filter", desc: "过滤（不计分）" }, { word: "exists", desc: "字段存在" },
  { word: "prefix", desc: "前缀匹配" }, { word: "wildcard", desc: "通配符匹配" },
  { word: "regexp", desc: "正则匹配" }, { word: "fuzzy", desc: "模糊匹配" },
  { word: "nested", desc: "嵌套对象查询" }, { word: "query_string", desc: "查询字符串" },
  { word: "simple_query_string", desc: "简易查询字符串" }, { word: "ids", desc: "按 ID 查询" },
  { word: "constant_score", desc: "固定评分" }, { word: "dis_max", desc: "最佳字段" },
  { word: "function_score", desc: "自定义评分" }, { word: "geo_distance", desc: "地理距离" },
  { word: "script", desc: "脚本" }, { word: "field", desc: "字段名" },
  { word: "fields", desc: "字段列表" }, { word: "value", desc: "值" },
  { word: "gt", desc: "大于" }, { word: "gte", desc: "大于等于" },
  { word: "lt", desc: "小于" }, { word: "lte", desc: "小于等于" },
  { word: "order", desc: "排序方向" }, { word: "minimum_should_match", desc: "最少匹配数" },
  { word: "operator", desc: "逻辑操作符" }, { word: "analyzer", desc: "分析器" },
  { word: "fuzziness", desc: "模糊度" }, { word: "boost", desc: "权重" },
  { word: "path", desc: "嵌套路径" }, { word: "inner_hits", desc: "嵌套命中" },
  { word: "avg", desc: "平均值聚合" }, { word: "sum", desc: "求和聚合" },
  { word: "min", desc: "最小值聚合" }, { word: "max", desc: "最大值聚合" },
  { word: "cardinality", desc: "去重计数聚合" }, { word: "date_histogram", desc: "日期直方图聚合" },
  { word: "histogram", desc: "直方图聚合" }, { word: "filters", desc: "过滤聚合" },
  { word: "top_hits", desc: "Top N 聚合" }, { word: "GET", desc: "HTTP GET" },
  { word: "PUT", desc: "HTTP PUT" }, { word: "POST", desc: "HTTP POST" },
  { word: "DELETE", desc: "HTTP DELETE" }, { word: "HEAD", desc: "HTTP HEAD" },
  { word: "_search", desc: "搜索接口" }, { word: "_cat", desc: "查看接口" },
  { word: "_mapping", desc: "映射接口" }, { word: "_settings", desc: "设置接口" },
  { word: "_doc", desc: "文档接口" }, { word: "_bulk", desc: "批量接口" },
  { word: "_refresh", desc: "刷新接口" },
  { word: "AND", desc: "KQL 与" }, { word: "OR", desc: "KQL 或" },
  { word: "NOT", desc: "KQL 非" }, { word: "TO", desc: "KQL 范围" },
];
const REDIS_COMMANDS: { word: string; desc: string }[] = [
  { word: "SCAN", desc: "迭代键" }, { word: "HSCAN", desc: "迭代哈希字段" },
  { word: "SSCAN", desc: "迭代集合" }, { word: "ZSCAN", desc: "迭代有序集合" },
  { word: "GET", desc: "读取字符串" }, { word: "MGET", desc: "批量读取" },
  { word: "SET", desc: "写入字符串" }, { word: "SETEX", desc: "带过期写入" },
  { word: "SETNX", desc: "不存在时写入" }, { word: "APPEND", desc: "追加字符串" },
  { word: "STRLEN", desc: "字符串长度" }, { word: "GETRANGE", desc: "字符串切片" },
  { word: "INCR", desc: "自增" }, { word: "DECR", desc: "自减" },
  { word: "INCRBY", desc: "按值自增" }, { word: "HGET", desc: "读取哈希字段" },
  { word: "HGETALL", desc: "读取整个哈希" }, { word: "HMGET", desc: "批量读取哈希字段" },
  { word: "HSET", desc: "写入哈希字段" }, { word: "HDEL", desc: "删除哈希字段" },
  { word: "HKEYS", desc: "哈希字段名" }, { word: "HVALS", desc: "哈希字段值" },
  { word: "LRANGE", desc: "列表区间" }, { word: "LLEN", desc: "列表长度" },
  { word: "LPUSH", desc: "左端插入" }, { word: "RPUSH", desc: "右端插入" },
  { word: "LPOP", desc: "左端弹出" }, { word: "RPOP", desc: "右端弹出" },
  { word: "SMEMBERS", desc: "集合成员" }, { word: "SISMEMBER", desc: "是否集合成员" },
  { word: "SCARD", desc: "集合大小" }, { word: "SADD", desc: "添加集合成员" },
  { word: "SREM", desc: "移除集合成员" }, { word: "ZRANGE", desc: "有序集合区间" },
  { word: "ZRANGEBYSCORE", desc: "按分数取区间" }, { word: "ZSCORE", desc: "成员分数" },
  { word: "ZCARD", desc: "有序集合大小" }, { word: "ZADD", desc: "添加有序成员" },
  { word: "KEYS", desc: "匹配键名" }, { word: "EXISTS", desc: "键是否存在" },
  { word: "TYPE", desc: "键类型" }, { word: "TTL", desc: "剩余过期秒数" },
  { word: "PTTL", desc: "剩余过期毫秒" }, { word: "EXPIRE", desc: "设置过期秒数" },
  { word: "PEXPIRE", desc: "设置过期毫秒" }, { word: "PERSIST", desc: "移除过期时间" },
  { word: "DBSIZE", desc: "键数量" }, { word: "INFO", desc: "服务信息" },
  { word: "RENAME", desc: "重命名键" }, { word: "DEL", desc: "删除键" },
  { word: "UNLINK", desc: "异步删除键" }, { word: "FLUSHDB", desc: "清空当前库" },
  { word: "OBJECT", desc: "对象信息" }, { word: "DUMP", desc: "序列化键" },
];
const REDIS_OPTIONS: { word: string; desc: string }[] = [
  { word: "MATCH", desc: "键名匹配模式" }, { word: "COUNT", desc: "每批数量" },
  { word: "WITHSCORES", desc: "返回分数" }, { word: "LIMIT", desc: "分页" },
  { word: "ASC", desc: "升序" }, { word: "DESC", desc: "降序" },
  { word: "ALPHA", desc: "按字典序" }, { word: "STORE", desc: "结果写入键" },
  { word: "KEEPTTL", desc: "保留过期时间" }, { word: "NX", desc: "不存在时写入" },
  { word: "XX", desc: "存在时写入" }, { word: "EX", desc: "过期秒数" },
  { word: "PX", desc: "过期毫秒" },
];
const WORD_DESC = new Map<string, string>();
for (const k of [...MONGO_WORDS, ...ES_WORDS, ...REDIS_COMMANDS, ...REDIS_OPTIONS]) WORD_DESC.set(k.word, k.desc);
const MONGO_WORD_SET = new Set(MONGO_WORDS.map((k) => k.word));
const ES_WORD_SET = new Set(ES_WORDS.map((k) => k.word));
const REDIS_COMMAND_SET = new Set(REDIS_COMMANDS.map((k) => k.word));
const REDIS_OPTION_SET = new Set(REDIS_OPTIONS.map((k) => k.word));

const TOKEN_RE = /(--[^\n]*|\/\*[\s\S]*?\*\/|#[^\n]*|'(?:\\.|''|[^'\\])*'|"(?:\\.|""|[^"\\])*"|`[^`]*`|\[[^\]]*\]|(?<![\p{L}\p{N}_$.])\d+(?:\.\d*)?(?![\p{L}\p{N}_$.])|(?<![\p{L}\p{N}_$])[\p{L}_][\p{L}\p{N}_$]*(?![\p{L}\p{N}_$])|\s+|[\s\S])/gu;
const JSON_RE = /("(?:\\.|[^"\\])*")|\b(true|false|null)\b|(\b\d+(?:\.\d+)?\b)|([\s\S])/g;
const IDENT_END_RE = /([\p{L}_][\p{L}\p{N}_$]*)$/u;
const QUALIFIER_RE = /([\p{L}_][\p{L}\p{N}_$]*)\s*\.\s*$/u;
const ALIAS_RE = /\b(?:from|join|update|into)\s+([`"[]?)([\p{L}_][\p{L}\p{N}_$.]*)\1?(?:\s+(?:as\s+)?([`"[]?)([\p{L}_][\p{L}\p{N}_$]*)\3)?/giu;

function tokenizeSQL(text: string, tables: Set<string>, columns: Set<string>): Token[] {
  const out: Token[] = [];
  for (const m of text.matchAll(TOKEN_RE)) {
    const s = m[0];
    if (!s) continue;
    let cls: TokenClass = "";
    let title: string | undefined;
    if (/^(--|\/\*|#)/.test(s)) cls = "tok-comment";
    else if (s[0] === "'") cls = "tok-str";
    else if (/^["`[]/.test(s)) cls = "tok-ident";
    else if (/^\d/.test(s)) cls = "tok-num";
    else if (/^[\p{L}_]/u.test(s)) {
      const upper = s.toUpperCase();
      if (KEYWORD_WORDS.has(upper)) { cls = "tok-kw"; title = KEYWORD_WORDS.get(upper); }
      else if (FUNCTIONS.has(upper)) { cls = "tok-fn"; title = "SQL 函数"; }
      else if (tables.has(s.toLowerCase())) { cls = "tok-table"; title = `表/视图：${s}`; }
      else if (columns.has(s.toLowerCase())) { cls = "tok-col"; title = `字段：${s}`; }
    }
    out.push({ text: s, cls, title });
  }
  return out;
}

function tokenizeJSON(text: string, words: Set<string>, names: Set<string>, columns: Set<string>): Token[] {
  const out: Token[] = [];
  for (const m of text.matchAll(JSON_RE)) {
    let cls: TokenClass = "";
    let title: string | undefined;
    if (m[1]) {
      const value = m[1].slice(1, -1);
      const isKey = text.slice((m.index ?? 0) + m[0].length).trimStart().startsWith(":");
      if (words.has(value)) { cls = "tok-kw"; title = WORD_DESC.get(value); }
      else if (isKey && columns.has(value.toLowerCase())) { cls = "tok-col"; title = `字段：${value}`; }
      else if (!isKey && names.has(value.toLowerCase())) { cls = "tok-table"; title = `对象：${value}`; }
      else cls = "tok-str";
    } else if (m[2]) cls = "tok-kw";
    else if (m[3]) cls = "tok-num";
    out.push({ text: m[0], cls, title });
  }
  return out;
}

function tokenizeRedis(text: string, commands: Set<string>, options: Set<string>, keys: Set<string>): Token[] {
  const out: Token[] = [];
  let commandSeen = false;
  for (const m of text.matchAll(/\S+|\s+/g)) {
    const s = m[0];
    if (/^\s+$/.test(s)) { out.push({ text: s, cls: "" }); continue; }
    let cls: TokenClass = "";
    let title: string | undefined;
    const clean = s.replace(/^["']|["']$/g, "");
    const upper = clean.toUpperCase();
    if (!commandSeen) {
      commandSeen = true;
      if (commands.has(upper)) { cls = "tok-kw"; title = WORD_DESC.get(upper); }
    } else if (options.has(upper)) { cls = "tok-kw"; title = WORD_DESC.get(upper); }
    else if (/^\d+$/.test(clean)) cls = "tok-num";
    else if (/^["']/.test(s)) cls = "tok-str";
    else if (keys.has(clean.toLowerCase().replace(/\*/g, ""))) cls = "tok-table";
    out.push({ text: s, cls, title });
  }
  return out;
}

const meta = ref<{ key: string; tables: TableMeta[] }>({ key: "", tables: [] });
const loadedColumns = ref<Record<string, ColumnInfo[]>>({});
const columnPending = new Map<string, Promise<void>>();
const composing = ref(false);
const scrollTop = ref(0);
const caret = ref({ left: 0, top: 0 });

const tableSet = computed(() => new Set(meta.value.tables.map((t) => t.name.split(".").pop()!.toLowerCase())));
const columnSet = computed(() => {
  const set = new Set<string>();
  for (const cols of Object.values(loadedColumns.value)) for (const c of cols) set.add(c.name.toLowerCase());
  return set;
});
const tokens = computed<Token[]>(() => {
  const text = props.modelValue;
  if (!text) return [];
  if (props.language === "sql") return tokenizeSQL(text, tableSet.value, columnSet.value);
  if (props.language === "redis") return tokenizeRedis(text, REDIS_COMMAND_SET, REDIS_OPTION_SET, tableSet.value);
  if (props.language === "es_dsl") return tokenizeJSON(text, ES_WORD_SET, tableSet.value, columnSet.value);
  if (props.language.startsWith("mongo")) return tokenizeJSON(text, MONGO_WORD_SET, tableSet.value, columnSet.value);
  return [{ text, cls: "" }];
});
const lineCount = computed(() => props.modelValue.split("\n").length);

interface SuggestState {
  open: boolean;
  start: number;
  end: number;
  prefix: string;
  qualifier: string;
  items: Suggestion[];
  active: number;
  loading: boolean;
}
const suggest = ref<SuggestState>({ open: false, start: 0, end: 0, prefix: "", qualifier: "", items: [], active: 0, loading: false });
const POPUP_W = 320;
const POPUP_H = 236;
const popupStyle = computed(() => {
  const w = wrap.value?.clientWidth ?? 800;
  const h = wrap.value?.clientHeight ?? 180;
  const left = Math.max(2, Math.min(caret.value.left, w - POPUP_W - 2));
  const below = caret.value.top + 24;
  const top = below + POPUP_H > h && caret.value.top > POPUP_H ? caret.value.top - POPUP_H - 4 : below;
  return { left: `${left}px`, top: `${Math.max(2, top)}px`, width: `${POPUP_W}px` };
});

function closeSuggest() {
  suggest.value.open = false;
}

let ctx: CanvasRenderingContext2D | null = null;
function updateCaret() {
  const el = ta.value;
  if (!el) return;
  const before = el.value.slice(0, el.selectionStart);
  const lines = before.split("\n");
  const cs = getComputedStyle(el);
  if (!ctx) ctx = document.createElement("canvas").getContext("2d");
  if (ctx) ctx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
  const x = ctx ? ctx.measureText(lines[lines.length - 1]).width : 0;
  const lh = parseFloat(cs.lineHeight) || 20;
  caret.value = {
    left: (parseFloat(cs.paddingLeft) || 0) + x - el.scrollLeft,
    top: (parseFloat(cs.paddingTop) || 0) + (lines.length - 1) * lh - el.scrollTop,
  };
}

function syncScroll() {
  const el = ta.value;
  const hi = layer.value;
  if (!el || !hi) return;
  scrollTop.value = el.scrollTop;
  hi.scrollTop = el.scrollTop;
  hi.scrollLeft = el.scrollLeft;
  updateCaret();
}

function sameIdent(a: string | undefined, b: string | undefined) {
  return !!a && !!b && a.toLowerCase() === b.toLowerCase();
}
function lastSegment(name: string) {
  const i = name.lastIndexOf(".");
  return i >= 0 ? name.slice(i + 1) : name;
}
function aliasPairs(text: string): Map<string, string> {
  const map = new Map<string, string>();
  for (const m of text.matchAll(ALIAS_RE)) {
    const name = m[2];
    const alias = m[4];
    if (alias && !ALIAS_STOP.has(alias.toUpperCase())) map.set(alias.toLowerCase(), name);
  }
  return map;
}
function findTable(qualifier: string, before: string): TableMeta | undefined {
  const direct = meta.value.tables.find((t) => sameIdent(t.name, qualifier) || sameIdent(lastSegment(t.name), qualifier));
  if (direct) return direct;
  const name = aliasPairs(before).get(qualifier.toLowerCase());
  return meta.value.tables.find((t) => sameIdent(t.name, name) || sameIdent(lastSegment(t.name), name));
}
function columnKey(t: TableMeta) {
  return `${meta.value.key}|${t.database}|${t.name}`;
}
async function ensureColumns(t: TableMeta | undefined) {
  // Redis 的 Describe 只返回固定的 key/type/ttl/value 列，对补全没有意义。
  if (!t || props.language === "redis") return;
  const key = columnKey(t);
  if (loadedColumns.value[key]) return;
  let pending = columnPending.get(key);
  if (!pending) {
    pending = api.describeObject(props.conn, {
      database: t.database,
      schema: t.schema && !t.name.includes(".") ? t.schema : undefined,
      name: t.name,
    }).then((res) => {
      loadedColumns.value = { ...loadedColumns.value, [key]: res.columns ?? [] };
    }).catch(() => {
      loadedColumns.value = { ...loadedColumns.value, [key]: [] };
    }).finally(() => columnPending.delete(key));
    columnPending.set(key, pending);
  }
  await pending;
}

let metaSeq = 0;
async function loadMeta() {
  const seq = ++metaSeq;
  closeSuggest();
  if (!props.conn) {
    meta.value = { key: "", tables: [] };
    return;
  }
  let dbs = store.dbCache[props.conn];
  if (!dbs) {
    try { dbs = await store.loadDatabases(props.conn); } catch { dbs = []; }
  }
  if (seq !== metaSeq) return;
  const db = props.database || store.connections.find((c) => c.name === props.conn)?.database || dbs?.[0]?.name || "";
  if (!db) return;
  const key = `${props.conn}|${db}`;
  if (meta.value.key === key) return;
  try {
    let objs;
    if (props.language === "es_dsl" && !props.database && dbs?.length) {
      // 连接默认查询会跨命名空间，索引提示同样汇总前几个命名空间。
      const groups = await Promise.all(dbs.slice(0, 8).map((d) => store.loadObjects(props.conn, d.name, "").catch(() => [])));
      const seen = new Set<string>();
      objs = groups.flat().filter((o) => {
        const k = `${o.database}|${o.name}`;
        if (seen.has(k)) return false;
        seen.add(k);
        return true;
      });
    } else {
      objs = await store.loadObjects(props.conn, db, "");
    }
    if (seq !== metaSeq) return;
    meta.value = {
      key,
      tables: objs
        .filter((o) => props.language === "redis"
          ? o.type === "key"
          : o.type === "table" || o.type === "view" || o.type === "collection")
        .map((o) => ({ name: o.name, schema: o.schema, database: o.database, type: o.type, comment: o.comment, rows: o.rows })),
    };
  } catch {
    if (seq === metaSeq) meta.value = { key, tables: [] };
  }
}

let suggestSeq = 0;
async function detectSuggestions(manual: boolean) {
  if (props.language === "redis") return detectRedisSuggestions(manual);
  if (props.language === "es_dsl") return detectJSONSuggestions("es", manual);
  if (props.language.startsWith("mongo")) return detectJSONSuggestions("mongo", manual);
  if (props.language !== "sql") { closeSuggest(); return; }
  const el = ta.value;
  if (!el) return;
  const seq = ++suggestSeq;
  const pos = el.selectionStart;
  const before = el.value.slice(0, pos);
  const match = before.match(IDENT_END_RE);
  const prefix = match?.[1] ?? "";
  const start = match ? pos - prefix.length : pos;
  if (!manual && !prefix) { closeSuggest(); return; }
  const qualifier = before.slice(0, start).match(QUALIFIER_RE)?.[1] ?? "";
  let items: Suggestion[] = [];

  if (qualifier) {
    suggest.value = { open: true, start, end: pos, prefix, qualifier, items: [], active: 0, loading: true };
    updateCaret();
    const table = findTable(qualifier, before);
    await ensureColumns(table);
    if (seq !== suggestSeq) return;
    items = table
      ? (loadedColumns.value[columnKey(table)] ?? []).map((c) => ({
          label: c.name,
          insert: c.name,
          kind: "column" as const,
          detail: [c.data_type || c.raw_type, c.primary_key ? "主键" : "", c.comment].filter(Boolean).join(" · "),
        }))
      : [];
  } else {
    const keywordItems: Suggestion[] = KEYWORDS.map((k) => ({ label: k.word, insert: k.word, kind: "keyword" as const, detail: k.desc }))
      .concat([...FUNCTIONS].map((f) => ({ label: f, insert: f, kind: "keyword" as const, detail: "SQL 函数" })));
    const tableItems: Suggestion[] = meta.value.tables.map((t) => ({
      label: t.name,
      insert: t.name,
      kind: "table" as const,
      detail: [t.type === "view" ? "视图" : "表", t.comment, t.rows !== undefined ? `${t.rows} 行` : ""].filter(Boolean).join(" · "),
    }));
    const dbItems: Suggestion[] = (store.dbCache[props.conn] ?? []).map((d) => ({
      label: d.name, insert: d.name, kind: "database" as const, detail: "数据库",
    }));
    const columnItems: Suggestion[] = [];
    for (const [key, cols] of Object.entries(loadedColumns.value)) {
      if (!key.startsWith(meta.value.key)) continue;
      const table = meta.value.tables.find((t) => columnKey(t) === key);
      if (!table) continue;
      for (const c of cols) {
        columnItems.push({
          label: c.name,
          insert: c.name,
          kind: "column",
          detail: `${table.name} · ${c.data_type || c.raw_type || "字段"}`,
        });
      }
      if (columnItems.length >= 40) break;
    }
    items = [...keywordItems, ...tableItems, ...dbItems, ...columnItems];
  }

  const needle = prefix.toLowerCase();
  const rank = (s: Suggestion) => {
    const label = s.label.toLowerCase();
    if (label === needle) return 0;
    if (label.startsWith(needle)) return 1;
    return 2;
  };
  const filtered = items
    .filter((s) => s.label.toLowerCase().includes(needle))
    .sort((a, b) => rank(a) - rank(b) || a.label.localeCompare(b.label))
    .slice(0, 60);
  if (seq !== suggestSeq) return;
  suggest.value = {
    open: filtered.length > 0 || manual,
    start, end: pos, prefix, qualifier, items: filtered, active: 0, loading: false,
  };
  updateCaret();
}

const wordSuggestion = (k: { word: string; desc: string }): Suggestion => ({
  label: k.word, insert: k.word, kind: "keyword", detail: k.desc,
});

interface JsonCtx { start: number; end: number; prefix: string; isKey: boolean; prevKey: string }
function jsonStringContext(text: string, pos: number): JsonCtx | null {
  const before = text.slice(0, pos);
  const open = before.match(/"((?:\\.|[^"\\])*)$/);
  if (open) {
    const quote = before.length - open[1].length - 1;
    return {
      start: quote + 1,
      end: pos,
      prefix: open[1],
      isKey: !before.slice(0, quote).trimEnd().endsWith(":"),
      prevKey: before.slice(0, quote).match(/"([^"]+)"\s*:\s*$/)?.[1] ?? "",
    };
  }
  const closed = before.match(/"((?:\\.|[^"\\])*)"\s*$/);
  if (closed) {
    const quote = before.length - closed[0].length;
    return {
      start: quote + 1,
      end: quote + closed[1].length + 1,
      prefix: closed[1],
      isKey: text.slice(pos).trimStart().startsWith(":"),
      prevKey: before.slice(0, quote).match(/"([^"]+)"\s*:\s*$/)?.[1] ?? "",
    };
  }
  return null;
}

function jsonContainerName(text: string, key: string) {
  return text.match(new RegExp(`"${key}"\\s*:\\s*"([^"]+)"`))?.[1] ?? "";
}
function findObject(name: string): TableMeta | undefined {
  return meta.value.tables.find((t) => sameIdent(t.name, name) || sameIdent(lastSegment(t.name), name));
}
function objectSuggestions(kind: "collection" | "index" | "key"): Suggestion[] {
  const label = kind === "key" ? "键" : kind === "index" ? "索引" : "集合";
  return meta.value.tables.map((t) => ({
    label: t.name,
    insert: t.name,
    kind: "table" as const,
    detail: [label, t.comment, t.rows !== undefined ? `${t.rows} 行` : ""].filter(Boolean).join(" · "),
  }));
}
function fieldSuggestions(t: TableMeta | undefined): Suggestion[] {
  if (!t) return [];
  return (loadedColumns.value[columnKey(t)] ?? []).map((c) => ({
    label: c.name,
    insert: c.name,
    kind: "column" as const,
    detail: [c.data_type || c.raw_type, c.comment].filter(Boolean).join(" · "),
  }));
}
function finishSuggestions(items: Suggestion[], prefix: string, start: number, end: number, manual: boolean, seq: number) {
  const needle = prefix.toLowerCase();
  const rank = (s: Suggestion) => {
    const label = s.label.toLowerCase();
    if (label === needle) return 0;
    if (label.startsWith(needle)) return 1;
    return 2;
  };
  const filtered = items
    .filter((s) => !needle || s.label.toLowerCase().includes(needle))
    .sort((a, b) => rank(a) - rank(b) || a.label.localeCompare(b.label))
    .slice(0, 60);
  if (seq !== suggestSeq) return;
  suggest.value = {
    open: filtered.length > 0 || manual,
    start, end, prefix, qualifier: "", items: filtered, active: 0, loading: false,
  };
  updateCaret();
}

async function detectJSONSuggestions(flavor: "mongo" | "es", manual: boolean) {
  const el = ta.value;
  if (!el) return;
  const seq = ++suggestSeq;
  const pos = el.selectionStart;
  const text = el.value;
  const before = text.slice(0, pos);
  const ctx = jsonStringContext(text, pos);
  const words = flavor === "mongo" ? MONGO_WORDS : ES_WORDS;

  if (!ctx) {
    // ES 的 Dev-Tools 命令（GET /_search 等）不是 JSON，按大写标识符补全。
    const m = before.match(/([\p{L}_][\p{L}\p{N}_]*)$/u);
    if (m && /^[A-Z_]+$/.test(m[1])) {
      finishSuggestions(words.map(wordSuggestion), m[1], pos - m[1].length, pos, manual, seq);
      return;
    }
    if (!manual) { closeSuggest(); return; }
    finishSuggestions(words.map(wordSuggestion), "", pos, pos, true, seq);
    return;
  }

  const { start, end, prefix, isKey, prevKey } = ctx;
  const containerKey = flavor === "mongo" ? "collection" : "index";
  const container = findObject(jsonContainerName(text, containerKey));
  let items: Suggestion[];

  if (!isKey && prevKey === containerKey) {
    items = objectSuggestions(flavor === "mongo" ? "collection" : "index");
  } else if (!isKey && flavor === "es" && ["field", "fields"].includes(prevKey)) {
    items = fieldSuggestions(container);
  } else if (isKey) {
    if (container) {
      suggest.value = { open: true, start, end, prefix, qualifier: "", items: [], active: 0, loading: true };
      await ensureColumns(container);
      if (seq !== suggestSeq) return;
    }
    items = [...words.map(wordSuggestion), ...fieldSuggestions(container)];
  } else {
    items = [];
  }
  finishSuggestions(items, prefix, start, end, manual, seq);
}

async function detectRedisSuggestions(manual: boolean) {
  const el = ta.value;
  if (!el) return;
  const seq = ++suggestSeq;
  const pos = el.selectionStart;
  const before = el.value.slice(0, pos);
  const match = before.match(/(?:^|\s)([^\s"']+)$/);
  const prefix = match?.[1] ?? "";
  const start = pos - prefix.length;
  if (!manual && !prefix) { closeSuggest(); return; }

  const isCommand = before.slice(0, start).trim() === "";
  let items: Suggestion[];
  let needle = prefix;
  if (isCommand) {
    items = REDIS_COMMANDS.map(wordSuggestion);
  } else {
    // MATCH user:* 这类通配符模式按前缀筛键名。
    const literal = prefix.split(/[*?]/)[0].toLowerCase();
    const keys = objectSuggestions("key").filter((s) => !literal || s.label.toLowerCase().startsWith(literal));
    items = [...REDIS_OPTIONS.map(wordSuggestion), ...keys];
    if (prefix.includes("*") || prefix.includes("?")) needle = "";
  }
  finishSuggestions(items, needle, start, pos, manual, seq);
}

function acceptSuggestion(i: number) {
  const s = suggest.value.items[i];
  const el = ta.value;
  if (!s || !el) return;
  const from = suggest.value.start;
  const to = Math.max(suggest.value.end, el.selectionStart);
  const text = props.modelValue;
  const jsonLang = props.language === "es_dsl" || props.language.startsWith("mongo");
  // 在尚未闭合的 JSON 字符串中补全时自动补上收尾引号。
  const insert = jsonLang && from > 0 && text[from - 1] === '"' && text[to] !== '"' ? `${s.insert}"` : s.insert;
  emit("update:modelValue", text.slice(0, from) + insert + text.slice(to));
  closeSuggest();
  nextTick(() => {
    const caretPos = from + insert.length;
    el.focus();
    el.setSelectionRange(caretPos, caretPos);
    updateCaret();
  });
}

function onInput(e: Event) {
  const el = e.target as HTMLTextAreaElement;
  emit("update:modelValue", el.value);
  if (!composing.value) void detectSuggestions(false);
}
function onCompositionStart() {
  composing.value = true;
  closeSuggest();
}
function onCompositionEnd(e: Event) {
  composing.value = false;
  onInput(e);
}
function onKeydown(e: KeyboardEvent) {
  if (e.ctrlKey && e.key === "Enter") { closeSuggest(); emit("run"); return; }
  if (e.ctrlKey && (e.code === "Space" || e.key === " ")) {
    e.preventDefault();
    void detectSuggestions(true);
    return;
  }
  if (suggest.value.open) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      suggest.value.active = suggest.value.items.length ? (suggest.value.active + 1) % suggest.value.items.length : 0;
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      suggest.value.active = suggest.value.items.length
        ? (suggest.value.active - 1 + suggest.value.items.length) % suggest.value.items.length
        : 0;
      return;
    }
    if (e.key === "Enter" || (e.key === "Tab" && !e.shiftKey)) {
      if (suggest.value.items.length) { e.preventDefault(); acceptSuggestion(suggest.value.active); return; }
      e.preventDefault();
      closeSuggest();
      return;
    }
    if (e.key === "Escape") { e.preventDefault(); closeSuggest(); return; }
  }
  if (e.key === "Tab") {
    e.preventDefault();
    const el = e.target as HTMLTextAreaElement;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    emit("update:modelValue", props.modelValue.slice(0, start) + "  " + props.modelValue.slice(end));
    nextTick(() => { el.selectionStart = el.selectionEnd = start + 2; });
  }
}
function onCursor() {
  updateCaret();
  if (suggest.value.open) void detectSuggestions(false);
}
function onKeyup(e: KeyboardEvent) {
  if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key) && suggest.value.open) void detectSuggestions(false);
}
function onDocMouseDown(e: MouseEvent) {
  if (suggest.value.open && wrap.value && !wrap.value.contains(e.target as Node)) closeSuggest();
}

function kindLabel(k: SuggestKind) {
  return k === "keyword" ? "关键字" : k === "table" ? "表" : k === "column" ? "字段" : "库";
}

watch(() => [props.conn, props.database, props.language], () => { void loadMeta(); }, { immediate: true });
watch(() => props.modelValue, () => nextTick(syncScroll));
onMounted(() => {
  document.addEventListener("mousedown", onDocMouseDown);
  nextTick(syncScroll);
});
onBeforeUnmount(() => document.removeEventListener("mousedown", onDocMouseDown));
defineExpose({ focus: () => ta.value?.focus() });
</script>

<template>
  <div class="editor-pane" :style="{ height: props.height + 'px' }">
    <div class="gutter code">
      <div class="gutter-inner" :style="{ transform: `translateY(${-scrollTop}px)` }">
        <div v-for="n in lineCount" :key="n">{{ n }}</div>
      </div>
    </div>
    <div ref="wrap" class="editor-input">
      <pre v-show="!composing" ref="layer" class="highlight code" aria-hidden="true"><span
        v-for="(t, i) in tokens" :key="i" :class="t.cls" :title="t.title">{{ t.text }}</span></pre>
      <textarea
        ref="ta"
        :value="props.modelValue"
        class="code sql-input"
        :class="{ composing }"
        wrap="off"
        spellcheck="false"
        :placeholder="props.placeholder"
        @scroll="syncScroll"
        @input="onInput"
        @compositionstart="onCompositionStart"
        @compositionend="onCompositionEnd"
        @keydown="onKeydown"
        @click="onCursor"
        @keyup="onKeyup"
      ></textarea>
      <div v-if="suggest.open" class="ac-pop" :style="popupStyle" @mousedown.prevent>
        <div v-if="suggest.loading" class="ac-empty">正在读取表字段…</div>
        <template v-else>
          <div
            v-for="(s, i) in suggest.items"
            :key="s.kind + ':' + s.label"
            class="ac-item"
            :class="{ active: i === suggest.active }"
            @mouseenter="suggest.active = i"
            @click="acceptSuggestion(i)"
          >
            <span class="ac-kind" :class="'k-' + s.kind">{{ kindLabel(s.kind) }}</span>
            <span class="ac-label">{{ s.label }}</span>
            <span class="ac-detail truncate">{{ s.detail }}</span>
          </div>
          <div v-if="!suggest.items.length" class="ac-empty">无匹配项</div>
          <div class="ac-foot">↑↓ 选择 · Enter 插入 · Esc 关闭 · Ctrl+Space 唤出</div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-pane { display: flex; min-height: 90px; border: 1px solid var(--border); border-top: 0; background: var(--bg); flex-shrink: 0; overflow: hidden; }
.gutter { width: 46px; flex-shrink: 0; overflow: hidden; padding: 10px 8px 10px 0; text-align: right; font-size: var(--fs-code); line-height: 20px; color: var(--text-faint); background: var(--surface); border-right: 1px solid var(--border); user-select: none; }
.gutter-inner > div { height: 20px; }
.editor-input { position: relative; flex: 1; min-width: 0; overflow: hidden; }
.highlight, .sql-input {
  position: absolute; inset: 0; width: 100%; height: 100%; margin: 0;
  padding: 10px 12px; border: 0; border-radius: 0;
  font-family: var(--mono); font-size: var(--fs-code); line-height: 20px;
  letter-spacing: 0; tab-size: 2; white-space: pre; box-shadow: none;
}
.highlight { z-index: 1; overflow: hidden; pointer-events: none; color: var(--text); background: transparent; }
.sql-input { z-index: 2; resize: none; overflow: auto; background: transparent; color: transparent; -webkit-text-fill-color: transparent; caret-color: var(--text); outline: none; }
.sql-input:focus { border: 0; box-shadow: none; outline: none; }
:root[data-density="compact"] .sql-input { padding: 10px 12px; }
.sql-input::placeholder { color: var(--text-faint); -webkit-text-fill-color: var(--text-faint); }
.sql-input.composing { color: var(--text); -webkit-text-fill-color: var(--text); }

.tok-kw { color: #c792ea; font-weight: 600; }
.tok-fn { color: #82aaff; }
.tok-str { color: #c3e88d; }
.tok-num { color: #f78c6c; }
.tok-comment { color: #6b7389; font-style: italic; }
.tok-ident { color: #ffcb6b; }
.tok-table { color: #4fc3f7; font-weight: 600; }
.tok-col { color: #a5d6ff; }
:root[data-theme="light"] .tok-kw { color: #7b31b8; }
:root[data-theme="light"] .tok-fn { color: #1f5fbf; }
:root[data-theme="light"] .tok-str { color: #247c46; }
:root[data-theme="light"] .tok-num { color: #b6531b; }
:root[data-theme="light"] .tok-comment { color: #8b95aa; }
:root[data-theme="light"] .tok-ident { color: #91600b; }
:root[data-theme="light"] .tok-table { color: #0a6e9e; }
:root[data-theme="light"] .tok-col { color: #175e8c; }

.ac-pop { position: absolute; z-index: 20; max-height: 236px; overflow: auto; padding: 4px; background: var(--surface-2); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); box-shadow: var(--shadow); font-family: var(--font); font-size: var(--fs-xs); }
.ac-item { display: flex; align-items: center; gap: 7px; padding: 5px 7px; border-radius: 4px; cursor: pointer; }
.ac-item.active { background: var(--accent-soft); }
.ac-kind { flex-shrink: 0; padding: 1px 4px; border-radius: 3px; background: var(--surface-3); color: var(--text-dim); font-size: 10px; }
.ac-kind.k-keyword { color: #c792ea; }
.ac-kind.k-table { color: #4fc3f7; }
.ac-kind.k-column { color: #82aaff; }
.ac-kind.k-database { color: #ffcb6b; }
.ac-label { flex-shrink: 0; font-family: var(--mono); color: var(--text); white-space: nowrap; }
.ac-detail { color: var(--text-faint); font-size: 10px; }
.ac-empty { padding: 10px; text-align: center; color: var(--text-faint); }
.ac-foot { margin-top: 3px; padding: 5px 7px 1px; border-top: 1px solid var(--border); color: var(--text-faint); font-size: 10px; }
</style>
