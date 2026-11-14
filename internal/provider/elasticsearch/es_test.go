package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/db-fly/db-fly/internal/domain"
)

type recordedRequest struct {
	Method      string
	Path        string
	Query       string
	Body        string
	ContentType string
	Auth        string
}

// fakeES 用 httptest 模拟 Elasticsearch REST API，测试无需真实集群。
type fakeES struct {
	mu       sync.Mutex
	reqs     []recordedRequest
	server   *httptest.Server
	bulkFail bool
}

func newFakeES(t *testing.T) *fakeES {
	t.Helper()
	f := &fakeES{}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeES) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recordedRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Body: string(body), ContentType: r.Header.Get("Content-Type"),
		Auth: r.Header.Get("Authorization"),
	})
	bulkFail := f.bulkFail
	f.mu.Unlock()

	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	writeError := func(status int, typ, reason string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"type": typ, "reason": reason}, "status": status,
		})
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodHead &&
		(strings.HasPrefix(path, "/_index_template/") || strings.HasPrefix(path, "/_template/")):
		writeError(404, "resource_not_found_exception", "template not found")
	case path == "/":
		writeJSON(map[string]any{
			"cluster_name": "test-cluster",
			"version":      map[string]any{"number": "8.12.0"},
		})
	case path == "/_cat/indices":
		writeJSON([]any{
			map[string]any{"index": "logs", "docs.count": "3", "store.size": "10kb", "health": "green"},
			map[string]any{"index": "shop.users", "docs.count": "2", "store.size": "5kb", "health": "yellow"},
			map[string]any{"index": ".internal", "docs.count": "1", "store.size": "1kb", "health": "green"},
		})
	case path == "/_cat/aliases":
		writeJSON([]any{map[string]any{"alias": "users_read", "index": "shop.users"}})
	case path == "/_data_stream":
		writeJSON(map[string]any{"data_streams": []any{}})
	case path == "/_index_template/dbfly_*":
		if r.Method == http.MethodGet {
			writeJSON(map[string]any{"index_templates": []any{
				map[string]any{"name": "dbfly_shop"},
			}})
			return
		}
		writeJSON(map[string]any{"acknowledged": true})
	case strings.HasPrefix(path, "/_index_template/"), strings.HasPrefix(path, "/_template/"):
		writeJSON(map[string]any{"acknowledged": true})
	case path == "/_bulk":
		actions := 0
		for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
			var obj map[string]any
			if json.Unmarshal([]byte(line), &obj) == nil {
				if _, ok := obj["index"]; ok {
					actions++
				}
				if _, ok := obj["update"]; ok {
					actions++
				}
				if _, ok := obj["delete"]; ok {
					actions++
				}
			}
		}
		items := make([]any, 0, actions)
		for i := 0; i < actions; i++ {
			item := map[string]any{"index": map[string]any{"_id": strconv.Itoa(i + 1), "status": 201}}
			if bulkFail && i == 0 {
				item = map[string]any{"index": map[string]any{
					"_id": "1", "status": 400,
					"error": map[string]any{"type": "mapper_parsing_exception", "reason": "bad field"},
				}}
			}
			items = append(items, item)
		}
		writeJSON(map[string]any{"took": 1, "errors": bulkFail, "items": items})
	case strings.HasSuffix(path, "/_search"):
		writeJSON(map[string]any{
			"took": 3, "timed_out": false,
			"_shards": map[string]any{"total": 1, "successful": 1, "failed": 0, "skipped": 0},
			"hits": map[string]any{
				"total": map[string]any{"value": 2, "relation": "eq"},
				"hits": []any{
					map[string]any{
						"_index": "shop.users", "_id": "1", "_score": 1.0,
						"_source": map[string]any{
							"name": "Alice", "age": 30, "tags": []any{"a", "b"},
							"addr": map[string]any{"city": "SZ"},
						},
					},
					map[string]any{"_index": "shop.users", "_id": "2", "_score": 1.0,
						"_source": map[string]any{"name": "Bob", "age": 25}},
				},
			},
			"aggregations": map[string]any{
				"by_city": map[string]any{"buckets": []any{
					map[string]any{"key": "SZ", "doc_count": 1},
				}},
			},
		})
	case strings.HasSuffix(path, "/_mapping"):
		index := strings.TrimSuffix(path, "/_mapping")
		index = strings.TrimPrefix(index, "/")
		writeJSON(map[string]any{index: map[string]any{"mappings": map[string]any{
			"dynamic": "strict",
			"properties": map[string]any{
				"name": map[string]any{"type": "text", "fields": map[string]any{
					"keyword": map[string]any{"type": "keyword"},
				}},
				"age":  map[string]any{"type": "long"},
				"addr": map[string]any{"properties": map[string]any{"city": map[string]any{"type": "keyword"}}},
			},
		}}})
	case strings.HasSuffix(path, "/_settings"):
		index := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/_settings")
		writeJSON(map[string]any{index: map[string]any{"settings": map[string]any{"index": map[string]any{
			"number_of_shards": "1", "number_of_replicas": "1", "refresh_interval": "1s",
			"uuid": "read-only-uuid", "creation_date": "1700000000",
		}}}})
	case strings.HasSuffix(path, "/_alias"):
		index := strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/_alias")
		writeJSON(map[string]any{index: map[string]any{"aliases": map[string]any{"users_read": map[string]any{}}}})
	case strings.HasSuffix(path, "/_refresh"):
		writeJSON(map[string]any{"_shards": map[string]any{"successful": 1}})
	case path == "/missing":
		writeError(404, "index_not_found_exception", "no such index [missing]")
	default:
		switch r.Method {
		case http.MethodHead:
			writeJSON(map[string]any{})
		case http.MethodPut, http.MethodDelete, http.MethodPost:
			writeJSON(map[string]any{"acknowledged": true})
		default:
			writeJSON(map[string]any{"acknowledged": true})
		}
	}
}

func (f *fakeES) last(t *testing.T) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("fakeES 没有收到任何请求")
	}
	return f.reqs[len(f.reqs)-1]
}

func (f *fakeES) find(t *testing.T, method, path string) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if f.reqs[i].Method == method && f.reqs[i].Path == path {
			return f.reqs[i]
		}
	}
	t.Fatalf("未找到请求 %s %s", method, path)
	return recordedRequest{}
}

func testSession(t *testing.T, f *fakeES, params map[string]string) domain.Session {
	t.Helper()
	u, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	if params == nil {
		params = map[string]string{}
	}
	if _, ok := params["namespaceSeparator"]; !ok {
		params["namespaceSeparator"] = "."
	}
	cfg := domain.ConnectionConfig{
		Name: "es-test", Kind: domain.KindElasticsearch,
		Host: host, Port: port, User: "elastic", Password: "secret",
		Params: params,
	}
	sess, err := providerImpl{}.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func strPtr(s string) *string { return &s }

func TestKindCapabilitiesAndDefaults(t *testing.T) {
	p := providerImpl{}
	if p.Kind() != domain.KindElasticsearch {
		t.Fatal("Kind 不正确")
	}
	if domain.DefaultPort(domain.KindElasticsearch) != 9200 {
		t.Fatal("默认端口应为 9200")
	}
	if !domain.KindElasticsearch.Valid() {
		t.Fatal("elasticsearch 应是合法 Kind")
	}
	caps := capabilitySet()
	for _, cap := range []domain.Capability{
		domain.CapESDSL, domain.CapJSONTree, domain.CapCreateDatabase,
		domain.CapCreateTable, domain.CapDataEdit, domain.CapMultiResult,
	} {
		if !caps.Has(cap) {
			t.Errorf("缺少能力 %s", cap)
		}
	}
	for _, cap := range []domain.Capability{
		domain.CapSQL, domain.CapTransactions, domain.CapSchemas, domain.CapTruncateTable,
	} {
		if caps.Has(cap) {
			t.Errorf("不应声明能力 %s", cap)
		}
	}
}

func TestNameMappingAndValidation(t *testing.T) {
	cfg := domain.ConnectionConfig{Params: map[string]string{"namespaceSeparator": "."}}
	if got := physicalIndex(cfg, "shop", "users"); got != "shop.users" {
		t.Fatalf("physicalIndex = %q", got)
	}
	if got := physicalIndex(cfg, "default", "logs"); got != "logs" {
		t.Fatalf("default 库不应加前缀: %q", got)
	}
	if got := physicalIndex(cfg, "shop", "shop.orders"); got != "shop.orders" {
		t.Fatalf("已带前缀不应重复: %q", got)
	}
	db, name := splitPhysicalIndex(cfg, "shop.users.v2")
	if db != "shop" || name != "users.v2" {
		t.Fatalf("splitPhysicalIndex = %q/%q", db, name)
	}
	if db, _ := splitPhysicalIndex(cfg, ".internal"); db != defaultDatabase {
		t.Fatalf("系统索引应归默认库，得到 %q", db)
	}
	noSep := domain.ConnectionConfig{Params: map[string]string{"namespaceSeparator": "none"}}
	if got := namespaceSeparator(noSep); got != "" {
		t.Fatalf("none 应关闭分组，得到 %q", got)
	}
	if got := physicalIndex(noSep, "shop", "users"); got != "users" {
		t.Fatalf("关闭分组后应返回原名，得到 %q", got)
	}
	for _, name := range []string{"", "Shop", "a/b", "a b", ".hidden", "-bad", "a#b"} {
		if err := validateName(name, "索引名"); err == nil {
			t.Errorf("validateName(%q) 应失败", name)
		}
	}
	if err := validateName("shop.users_v2", "索引名"); err != nil {
		t.Fatalf("合法名称被拒绝: %v", err)
	}
	for _, expr := range []string{"", "../etc", "a?b", "a/b", "a b"} {
		if err := validateIndexExpr(expr); err == nil {
			t.Errorf("validateIndexExpr(%q) 应失败", expr)
		}
	}
	if err := validateIndexExpr("*,-.*,logs-*"); err != nil {
		t.Fatalf("合法索引表达式被拒绝: %v", err)
	}
}

func TestClientConfiguration(t *testing.T) {
	cfg := domain.ConnectionConfig{
		Host: "es.example.com", Port: 9200, User: "u", Password: "p",
		TLSMode: "true",
		Params: map[string]string{
			"pathPrefix": "/proxy", "apiVersion": "2023-10-31", "auth": "basic",
		},
	}
	c, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.base != "https://es.example.com:9200/proxy" {
		t.Fatalf("base = %q", c.base)
	}
	if c.authHeader != "Basic dTpw" {
		t.Fatalf("auth = %q", c.authHeader)
	}
	if c.apiVersion != "2023-10-31" {
		t.Fatalf("apiVersion = %q", c.apiVersion)
	}
	apiKey := cfg
	apiKey.Params = map[string]string{"auth": "apikey"}
	ak, err := newClient(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ak.Close()
	if ak.authHeader != "ApiKey p" {
		t.Fatalf("apikey auth = %q", ak.authHeader)
	}
	if !versionAtLeast("8.12.0", 7, 8) || versionAtLeast("7.7.1", 7, 8) {
		t.Fatal("索引模板版本判断不正确")
	}
	legacy := &esClient{composableTemplates: false}
	if legacy.templatePath("shop") != "/_template/dbfly_shop" {
		t.Fatal("7.7 及以前应使用 legacy 模板端点")
	}
}

func TestParseSearchSpec(t *testing.T) {
	cfg := domain.ConnectionConfig{Params: map[string]string{"namespaceSeparator": "."}}
	q := domain.Query{
		Language: domain.LangESDSL,
		Text:     `{"index":"shop.users","query":{"match":{"name":"Alice"}},"size":5000}`,
		MaxRows:  100,
	}
	spec, err := parseSearchSpec(q, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Index != "shop.users" {
		t.Fatalf("index = %q", spec.Index)
	}
	if got := numberFromAny(spec.Body["size"]); got != 101 {
		t.Fatalf("size 应被限制为 MaxRows+1，得到 %d", got)
	}
	if asMap(spec.Body["query"])["match"] == nil {
		t.Fatal("DSL query 丢失")
	}
	// 纯文本 → query_string
	spec, err = parseSearchSpec(domain.Query{Text: "title:go AND status:paid", MaxRows: 10}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Index != "*,-.*" {
		t.Fatalf("默认索引表达式 = %q", spec.Index)
	}
	qs := asMap(asMap(spec.Body["query"])["query_string"])
	if qs["query"] != "title:go AND status:paid" {
		t.Fatalf("query_string = %v", qs["query"])
	}
	// 命名空间 + 结构化过滤与排序
	spec, err = parseSearchSpec(domain.Query{
		Database: "shop", Ref: domain.ObjectRef{Database: "shop", Name: "users"},
		Conditions: []domain.FilterCondition{
			{Column: "age", Operator: ">=", Value: "18"},
			{Column: "name", Operator: "LIKE", Value: "Al%", Join: "OR"},
		},
		OrderTerms: []domain.SortTerm{{Column: "age", Desc: true}},
		Offset:     20, MaxRows: 25,
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Index != "shop.users" {
		t.Fatalf("表浏览应指向物理索引，得到 %q", spec.Index)
	}
	if numberFromAny(spec.Body["from"]) != 20 || numberFromAny(spec.Body["size"]) != 26 {
		t.Fatalf("分页 from/size = %v/%v", spec.Body["from"], spec.Body["size"])
	}
	sorts := toSlice(spec.Body["sort"])
	if len(sorts) != 1 || asMap(asMap(sorts[0])["age"])["order"] != "desc" {
		t.Fatalf("sort = %v", spec.Body["sort"])
	}
	boolQuery := asMap(asMap(spec.Body["query"])["bool"])
	if len(toSlice(boolQuery["should"])) != 2 {
		t.Fatalf("OR 条件应生成两个 should 分支: %v", boolQuery)
	}
}

func TestBuildESFilterOperators(t *testing.T) {
	filter, err := buildESFilter([]domain.FilterCondition{
		{Column: "status", Operator: "IN", Value: "paid, done"},
		{Column: "deleted_at", Operator: "IS NULL"},
		{Column: "price", Operator: "<", Value: "100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	clauses := toSlice(asMap(filter["bool"])["must"])
	if len(clauses) != 3 {
		t.Fatalf("应有 3 个 must 子句，得到 %d", len(clauses))
	}
	if asMap(asMap(clauses[0])["terms"])["status"] == nil {
		t.Fatal("IN 应生成 terms")
	}
	if asMap(asMap(clauses[1])["bool"])["must_not"] == nil {
		t.Fatal("IS NULL 应生成 must_not exists")
	}
	if asMap(asMap(asMap(clauses[2])["range"])["price"])["lt"] == nil {
		t.Fatal("< 应生成 range lt")
	}
	if _, err := buildESFilter([]domain.FilterCondition{{Column: "", Operator: "="}}); err == nil {
		t.Fatal("缺字段应报错")
	}
	if _, err := conditionToES(domain.FilterCondition{Column: "a", Operator: "REGEXP", Value: "x"}); err == nil {
		t.Fatal("不支持的操作符应报错")
	}
}

func TestCommandHelpers(t *testing.T) {
	cmd := esCommand{Action: "createTable", Database: "shop", Collection: "users", Options: map[string]any{"x": 1}}
	text, err := encodeCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCommand(text)
	if err != nil || decoded.Action != "createTable" || decoded.Collection != "users" {
		t.Fatalf("命令往返失败: %v %+v", err, decoded)
	}
	if _, err := decodeCommand(`{"options":{}}`); err == nil {
		t.Fatal("缺少 action/path 应报错")
	}
	body := createIndexBody(map[string]any{
		"number_of_shards": 2, "dynamic": "strict",
		"aliases": map[string]any{"users_read": map[string]any{}},
	}, []domain.ColumnSpec{
		{Name: "title", Type: "VARCHAR(255)"},
		{Name: "age", Type: "INT"},
		{Name: "_id", Type: "text"},
		{Name: "nested.path", Type: "text"},
	})
	settings := asMap(body["settings"])
	if numberFromAny(settings["number_of_shards"]) != 2 {
		t.Fatalf("settings = %v", settings)
	}
	props := asMap(asMap(body["mappings"])["properties"])
	if len(props) != 2 || asMap(props["age"])["type"] != "long" {
		t.Fatalf("properties = %v", props)
	}
	if asMap(props["title"])["type"] != "text" {
		t.Fatalf("文本字段应为 text: %v", props["title"])
	}
	if asMap(body["aliases"])["users_read"] == nil {
		t.Fatal("别名丢失")
	}
	var cols []domain.ColumnInfo
	columnsFromMappings(props, "", 0, &cols)
	if len(cols) != 2 {
		t.Fatalf("columnsFromMappings = %d", len(cols))
	}
	c := &esClient{composableTemplates: true, cfg: domain.ConnectionConfig{
		Params: map[string]string{"namespaceSeparator": "."},
	}}
	tpl := templateBody(c, "shop", map[string]any{"number_of_shards": 3})
	if patterns := toSlice(tpl["index_patterns"]); len(patterns) != 1 || patterns[0] != "shop.*" {
		t.Fatalf("index_patterns = %v", patterns)
	}
	if asMap(asMap(tpl["template"])["settings"])["number_of_shards"] != 3 {
		t.Fatalf("模板 settings = %v", tpl["template"])
	}
	legacy := templateBody(&esClient{composableTemplates: false, cfg: c.cfg},
		"shop", map[string]any{"number_of_replicas": 1})
	if legacy["settings"] == nil || legacy["template"] != nil || legacy["order"] == nil {
		t.Fatalf("legacy 模板结构不正确: %v", legacy)
	}
	if !strings.Contains(string(mustJSON(t, esFieldType("BOOL"))), "boolean") {
		t.Fatal("布尔类型映射错误")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenServerInfoAndAuth(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	info, err := sess.ServerInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "8.12.0" || info.Edition != "test-cluster" {
		t.Fatalf("ServerInfo = %+v", info)
	}
	root := f.find(t, http.MethodGet, "/")
	if root.Auth != "Basic ZWxhc3RpYzpzZWNyZXQ=" {
		t.Fatalf("Basic 认证头 = %q", root.Auth)
	}
	if sess.Maintenance() != nil {
		t.Fatal("ES 不提供 TableMaintenance")
	}
}

func TestMetadataListAndDescribe(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	ctx := context.Background()
	dbs, err := sess.Metadata().ListDatabases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range dbs {
		names[d.Name] = true
	}
	if !names[defaultDatabase] || !names["shop"] {
		t.Fatalf("库列表应含 default 与 shop: %v", dbs)
	}
	if names[".internal"] {
		t.Fatal("系统索引前缀不应成为独立逻辑库")
	}
	schemas, err := sess.Metadata().ListSchemas(ctx, "shop")
	if err != nil || len(schemas) != 0 {
		t.Fatalf("ES 无 schema 层级: %v %v", schemas, err)
	}
	objects, err := sess.Metadata().ListObjects(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range objects {
		if o.Name == "users" && o.Type == domain.ObjectCollection {
			found = true
			if o.Rows != 2 {
				t.Fatalf("Rows = %d", o.Rows)
			}
		}
	}
	if !found {
		t.Fatalf("未找到索引 users: %+v", objects)
	}
	schema, err := sess.Metadata().Describe(ctx, domain.ObjectRef{Database: "shop", Name: "users"})
	if err != nil {
		t.Fatal(err)
	}
	hasColumn := func(name string) bool {
		for _, c := range schema.Columns {
			if c.Name == name {
				return true
			}
		}
		return false
	}
	if len(schema.Columns) < 4 || !hasColumn("name") || !hasColumn("addr.city") || !hasColumn("age") {
		t.Fatalf("mapping 展开列 = %+v", schema.Columns)
	}
	if !strings.Contains(schema.Engine, "shards=1") || !strings.Contains(schema.Engine, "replicas=1") {
		t.Fatalf("Engine = %q", schema.Engine)
	}
	if !strings.Contains(schema.Comment, "dynamic=strict") || !strings.Contains(schema.Comment, "users_read") {
		t.Fatalf("Comment = %q", schema.Comment)
	}
	if !strings.HasPrefix(schema.DDL, "PUT /shop.users") || strings.Contains(schema.DDL, "read-only-uuid") {
		t.Fatalf("ShowCreate 应剔除只读设置: %s", schema.DDL)
	}
}

func TestCreateDatabaseAndIndexScripts(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	ctx := context.Background()
	editor := sess.Schema()

	dbScript, err := editor.CreateDatabaseScript(ctx, domain.DatabaseSpec{
		Name: "shop", Options: `{"number_of_shards":2,"number_of_replicas":0}`, IfNotExists: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dbScript.Format != scriptFormat || len(dbScript.Statements) != 1 || len(dbScript.Rollback) != 1 {
		t.Fatalf("建库脚本 = %+v", dbScript)
	}
	if _, err := editor.Apply(ctx, dbScript); err != nil {
		t.Fatal(err)
	}
	put := f.find(t, http.MethodPut, "/_index_template/dbfly_shop")
	if !strings.Contains(put.Body, "shop.*") || !strings.Contains(put.Body, `"number_of_shards":2`) {
		t.Fatalf("模板请求体 = %s", put.Body)
	}

	tableScript, err := editor.CreateTableScript(ctx, domain.TableDefinition{
		Ref: domain.ObjectRef{Database: "shop", Name: "users"},
		Columns: []domain.ColumnSpec{
			{Name: "name", Type: "VARCHAR(100)"},
			{Name: "age", Type: "INT"},
		},
		Options: `{"number_of_shards":1,"dynamic":"strict"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tableScript.Warnings) == 0 {
		t.Fatal("建索引脚本应附带 warnings")
	}
	if _, err := editor.Apply(ctx, tableScript); err != nil {
		t.Fatal(err)
	}
	create := f.find(t, http.MethodPut, "/shop.users")
	if !strings.Contains(create.Body, `"properties"`) || !strings.Contains(create.Body, `"dynamic":"strict"`) {
		t.Fatalf("建索引请求体 = %s", create.Body)
	}
	ddl, err := editor.ShowCreate(ctx, domain.ObjectRef{Database: "shop", Name: "users"})
	if err != nil || !strings.HasPrefix(ddl, "PUT /shop.users") {
		t.Fatalf("ShowCreate = %q %v", ddl, err)
	}
	drop, err := editor.DropObjectScript(ctx, domain.ObjectRef{Database: "shop", Name: "users"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Apply(ctx, drop); err != nil {
		t.Fatal(err)
	}
	f.find(t, http.MethodDelete, "/shop.users")

	alter, err := editor.AlterTableScript(ctx,
		domain.ObjectRef{Database: "shop", Name: "users"},
		[]domain.SchemaChange{{Kind: domain.ChangeAddColumn, Column: domain.ColumnSpec{Name: "email", Type: "KEYWORD"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Apply(ctx, alter); err != nil {
		t.Fatal(err)
	}
	mapping := f.find(t, http.MethodPut, "/shop.users/_mapping")
	if !strings.Contains(mapping.Body, `"type":"keyword"`) {
		t.Fatalf("put mapping = %s", mapping.Body)
	}
	if _, err := editor.AlterTableScript(ctx,
		domain.ObjectRef{Database: "shop", Name: "users"},
		[]domain.SchemaChange{{Kind: domain.ChangeRenameTable, NewName: "orders"}}); err == nil {
		t.Fatal("ES 重命名索引应返回 unsupported")
	}
}

func TestExecuteSearchAndResultSets(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	ctx := context.Background()
	rs, err := sess.Query().Execute(ctx, domain.Query{
		Language: domain.LangESDSL, Text: `{"index":"shop.users","query":{"match_all":{}}}`, MaxRows: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	search := f.find(t, http.MethodPost, "/shop.users/_search")
	if !strings.Contains(search.Body, `"size":101`) {
		t.Fatalf("_search body = %s", search.Body)
	}
	cols := rs.Columns()
	if len(cols) == 0 || cols[0].Name != "_id" {
		t.Fatalf("首列应为 _id: %+v", cols)
	}
	count := 0
	for rs.Next() {
		count++
		row := rs.Row()
		if len(row) != len(cols) {
			t.Fatalf("行列数不一致: %d/%d", len(row), len(cols))
		}
		doc := rs.Document()
		if !strings.Contains(doc, `"_id":"`+strconv.Itoa(count)+`"`) {
			t.Fatalf("原始文档缺少 _id: %s", doc)
		}
		if !strings.Contains(doc, `"tags":["a","b"]`) && count == 1 {
			t.Fatalf("嵌套数组丢失: %s", doc)
		}
	}
	if count != 2 || rs.Err() != nil {
		t.Fatalf("命中数 = %d, err = %v", count, rs.Err())
	}
	if len(rs.Messages()) == 0 {
		t.Fatal("缺少服务端摘要信息")
	}
	if !rs.NextResultSet() {
		t.Fatal("聚合结果应作为第二个结果集")
	}
	aggCols := rs.Columns()
	if len(aggCols) != 3 || aggCols[0].Name != "aggregation" {
		t.Fatalf("聚合列 = %+v", aggCols)
	}
	if !rs.Next() || rs.Row()[1].Text != "SZ" {
		t.Fatalf("聚合桶 = %+v", rs.Row())
	}
	plan, err := sess.Query().Explain(ctx, domain.Query{
		Language: domain.LangESDSL, Text: `{"index":"shop.users","query":{"match_all":{}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Next() || !strings.Contains(plan.Document(), `"explanation"`) {
		t.Fatal("Explain 应返回含 explanation 的文档")
	}
	if _, err := sess.Query().Execute(ctx, domain.Query{Language: domain.LangSQL, Text: "SELECT 1"}); err == nil {
		t.Fatal("ES 模块应拒绝 SQL 语言")
	}
}

func TestExecRESTCommand(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	res, err := sess.Query().Exec(context.Background(),
		`{"method":"PUT","path":"/{index}/_settings","body":{"index":{"number_of_replicas":0}}}`,
		"shop.users")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "shop.users/_settings") {
		t.Fatalf("Exec 结果 = %+v", res)
	}
	f.find(t, http.MethodPut, "/shop.users/_settings")
}

func TestDataEditorBulkAndLosslessDocuments(t *testing.T) {
	f := newFakeES(t)
	sess := testSession(t, f, nil)
	ctx := context.Background()
	editor := sess.Data()
	ref := domain.ObjectRef{Database: "shop", Name: "users"}
	text := `{"_id":"1001","name":"Alice","age":30,"code":"007","tags":["a","b"],"addr":{"city":"SZ"},"active":true}`
	req := domain.EditRequest{
		Ref: ref,
		Edits: []domain.RowEdit{
			{Kind: domain.EditInsert, Values: map[string]*string{"_id": strPtr("1001"), "name": strPtr("Alice")}},
		},
		Documents: []string{text},
	}
	previews, err := editor.Preview(ctx, req)
	if err != nil || len(previews) != 1 {
		t.Fatalf("Preview = %v %v", previews, err)
	}
	if !strings.Contains(previews[0], `"_id":"1001"`) {
		t.Fatalf("预览未提取 _id: %s", previews[0])
	}
	result, err := editor.Apply(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 1 {
		t.Fatalf("Applied = %d", result.Applied)
	}
	bulk := f.find(t, http.MethodPost, "/_bulk")
	if bulk.ContentType != "application/x-ndjson" || bulk.Query != "refresh=true" {
		t.Fatalf("bulk 请求 = %+v", bulk)
	}
	if !strings.Contains(bulk.Body, `"_index":"shop.users"`) {
		t.Fatalf("bulk 目标索引错误: %s", bulk.Body)
	}
	// 原始文档通道必须保留标量类型与嵌套结构，不做字符串化。
	if !strings.Contains(bulk.Body, `"code":"007"`) ||
		!strings.Contains(bulk.Body, `"age":30`) ||
		!strings.Contains(bulk.Body, `"active":true`) ||
		!strings.Contains(bulk.Body, `"tags":["a","b"]`) ||
		strings.Contains(bulk.Body, `"_id":"1001","name"`) {
		t.Fatalf("无损文档写入失败: %s", bulk.Body)
	}

	// 无原始文档时（CSV 导入）走 parseScalar 还原。
	_, err = editor.Apply(ctx, domain.EditRequest{
		Ref: ref,
		Edits: []domain.RowEdit{{Kind: domain.EditInsert, Values: map[string]*string{
			"_id": strPtr("2002"), "name": strPtr("Bob"), "age": strPtr("25"),
			"meta": strPtr(`{"city":"SZ"}`),
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bulk = f.find(t, http.MethodPost, "/_bulk")
	if !strings.Contains(bulk.Body, `"age":25`) || !strings.Contains(bulk.Body, `"meta":{"city":"SZ"}`) {
		t.Fatalf("CSV 值还原失败: %s", bulk.Body)
	}

	updates := domain.EditRequest{Ref: ref, Edits: []domain.RowEdit{
		{Kind: domain.EditUpdate, Key: map[string]*string{"_id": strPtr("1001")},
			Values: map[string]*string{"name": strPtr("Alice2")}},
		{Kind: domain.EditDelete, Key: map[string]*string{"_id": strPtr("2002")}},
	}}
	if _, err := editor.Apply(ctx, updates); err != nil {
		t.Fatal(err)
	}
	bulk = f.find(t, http.MethodPost, "/_bulk")
	if !strings.Contains(bulk.Body, `"update"`) || !strings.Contains(bulk.Body, `"delete"`) {
		t.Fatalf("update/delete bulk = %s", bulk.Body)
	}
	if _, err := editor.Apply(ctx, domain.EditRequest{Ref: ref,
		Edits: []domain.RowEdit{{Kind: domain.EditUpdate, Values: map[string]*string{"name": strPtr("x")}}},
	}); err == nil {
		t.Fatal("更新缺少 _id 应报错")
	}

	f.bulkFail = true
	if _, err := editor.Apply(ctx, req); err == nil {
		t.Fatal("bulk 部分失败应返回错误")
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		code   domain.ErrorCode
	}{
		{401, domain.CodeAuthFailed},
		{403, domain.CodePermission},
		{404, domain.CodeNotFound},
		{429, domain.CodeTimeout},
		{400, domain.CodeValidation},
		{500, domain.CodeQuery},
	}
	for _, c := range cases {
		err := mapQueryError(&esHTTPError{Status: c.status, Type: "some_error", Reason: "boom"})
		if got := domain.CodeOf(err); got != c.code {
			t.Errorf("status %d → %s，期望 %s", c.status, got, c.code)
		}
	}
	if got := domain.CodeOf(mapOpenError(&esHTTPError{Status: 401, Reason: "no"})); got != domain.CodeAuthFailed {
		t.Errorf("Open 401 → %s", got)
	}
	if got := domain.CodeOf(mapQueryError(context.Canceled)); got != domain.CodeCanceled {
		t.Errorf("取消 → %s", got)
	}
	if err := mapQueryError(nil); err != nil {
		t.Fatal("nil 错误应返回 nil")
	}
	var parsed *esHTTPError
	raw := []byte(`{"error":{"type":"index_not_found_exception","reason":"no such index [x]","index":"x"},"status":404}`)
	if !errors.As(parseESError(raw, 404), &parsed) || parsed.Type != "index_not_found_exception" {
		t.Fatal("ES 错误体解析失败")
	}
}
