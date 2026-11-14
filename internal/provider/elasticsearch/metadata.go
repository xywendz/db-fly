package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/db-fly/db-fly/internal/domain"
)

type metadataBrowser struct {
	client *esClient
	cfg    domain.ConnectionConfig
}

type catIndex struct {
	Index     string `json:"index"`
	DocsCount any    `json:"docs.count"`
	StoreSize string `json:"store.size"`
	Status    string `json:"status"`
	Health    string `json:"health"`
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	names := map[string]bool{defaultDatabase: true}
	var indices []catIndex
	if _, err := b.client.jsonCall(ctx, http.MethodGet,
		"/_cat/indices?format=json&h=index", nil, &indices); err != nil {
		return nil, mapQueryError(err)
	}
	for _, item := range indices {
		if isSystemIndex(item.Index) && !showSystemIndices(b.cfg) {
			continue
		}
		db, _ := splitPhysicalIndex(b.cfg, item.Index)
		names[db] = true
	}
	for _, db := range b.templateNames(ctx) {
		names[db] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	out := make([]domain.DatabaseInfo, 0, len(sorted))
	for _, name := range sorted {
		out = append(out, domain.DatabaseInfo{Name: name, System: isSystemIndex(name)})
	}
	return out, nil
}

// templateNames 读取 db-fly 管理的索引模板，使「尚无索引的空库」仍出现在资源树中。
func (b *metadataBrowser) templateNames(ctx context.Context) []string {
	var out []string
	if b.client.composableTemplates {
		var resp struct {
			IndexTemplates []struct {
				Name string `json:"name"`
			} `json:"index_templates"`
		}
		if _, err := b.client.jsonCall(ctx, http.MethodGet,
			"/_index_template/"+templatePrefix+"*", nil, &resp); err != nil {
			return nil
		}
		for _, t := range resp.IndexTemplates {
			if name := strings.TrimPrefix(t.Name, templatePrefix); name != "" {
				out = append(out, name)
			}
		}
		return out
	}
	var resp map[string]any
	if _, err := b.client.jsonCall(ctx, http.MethodGet,
		"/_template/"+templatePrefix+"*", nil, &resp); err != nil {
		return nil
	}
	for name := range resp {
		if n := strings.TrimPrefix(name, templatePrefix); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// ListSchemas 在 Elasticsearch 中无独立 schema 层级。
func (b *metadataBrowser) ListSchemas(context.Context, string) ([]string, error) {
	return nil, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, _ string) ([]domain.ObjectInfo, error) {
	if strings.TrimSpace(database) == "" {
		database = defaultDatabase
	}
	var indices []catIndex
	if _, err := b.client.jsonCall(ctx, http.MethodGet,
		"/_cat/indices?format=json&h=index,docs.count,store.size,status,health", nil, &indices); err != nil {
		return nil, mapQueryError(err)
	}
	seen := make(map[string]bool)
	out := make([]domain.ObjectInfo, 0, len(indices))
	for _, item := range indices {
		if isSystemIndex(item.Index) && !showSystemIndices(b.cfg) {
			continue
		}
		db, name := splitPhysicalIndex(b.cfg, item.Index)
		if db != database {
			continue
		}
		key := string(domain.ObjectCollection) + ":" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		comment := fmt.Sprintf("%s · health=%s · store=%s", item.Index, item.Health, item.StoreSize)
		out = append(out, domain.ObjectInfo{
			Database: database, Name: name, Type: domain.ObjectCollection,
			Comment: comment, Rows: numberFromAny(item.DocsCount),
		})
	}
	// 数据流与别名是 ES 特有的可查询目标，分别以集合 / 视图节点展示。
	var streams struct {
		DataStreams []struct {
			Name string `json:"name"`
		} `json:"data_streams"`
	}
	if _, err := b.client.jsonCall(ctx, http.MethodGet, "/_data_stream", nil, &streams); err == nil {
		for _, s := range streams.DataStreams {
			db, name := splitPhysicalIndex(b.cfg, s.Name)
			if db != database {
				continue
			}
			key := string(domain.ObjectCollection) + ":" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, domain.ObjectInfo{
				Database: database, Name: name, Type: domain.ObjectCollection,
				Comment: s.Name + " · data stream",
			})
		}
	}
	var aliases []struct {
		Alias string `json:"alias"`
		Index string `json:"index"`
	}
	if _, err := b.client.jsonCall(ctx, http.MethodGet,
		"/_cat/aliases?format=json&h=alias,index", nil, &aliases); err == nil {
		for _, a := range aliases {
			db, name := splitPhysicalIndex(b.cfg, a.Alias)
			if db != database {
				continue
			}
			key := string(domain.ObjectView) + ":" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, domain.ObjectInfo{
				Database: database, Name: name, Type: domain.ObjectView,
				Comment: "alias → " + a.Index,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	physical := physicalIndex(b.cfg, ref.Database, ref.Name)
	if physical == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "es.metadata",
			"未指定数据库或索引")
	}
	if err := validateIndexExpr(physical); err != nil {
		return domain.TableSchema{}, err
	}
	result := domain.TableSchema{
		Ref:     domain.ObjectRef{Database: ref.Database, Name: ref.Name},
		Indexes: []domain.IndexInfo{{Name: "_id_", Columns: []string{"_id"}, Primary: true, Unique: true}},
		Comment: "物理索引: " + physical,
	}
	def, err := fetchIndexDefinition(ctx, b.client, physical)
	if err != nil {
		return result, mapQueryError(err)
	}
	properties := asMap(asMap(def["mappings"])["properties"])
	columns := make([]domain.ColumnInfo, 0, maxProjectionColumns)
	if len(properties) > 0 {
		columnsFromMappings(properties, "", 0, &columns)
	}
	if len(columns) == 0 {
		// 动态映射的索引可能没有显式 properties，退化为采样推断。
		columns, err = sampleColumns(ctx, b.client, physical)
		if err != nil {
			return result, err
		}
	}
	result.Columns = columns
	settings := asMap(def["settings"])
	result.Engine = fmt.Sprintf("shards=%v replicas=%v refresh=%v",
		settings["number_of_shards"], settings["number_of_replicas"], settings["refresh_interval"])
	if dynamic, ok := asMap(def["mappings"])["dynamic"]; ok {
		result.Comment += fmt.Sprintf(" · dynamic=%v", dynamic)
	}
	if aliases := asMap(def["aliases"]); len(aliases) > 0 {
		keys := make([]string, 0, len(aliases))
		for k := range aliases {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		result.Comment += " · aliases=" + strings.Join(keys, ",")
	}
	if ddl, ddlErr := (&schemaEditor{client: b.client, cfg: b.cfg}).ShowCreate(ctx, ref); ddlErr == nil {
		result.DDL = ddl
	}
	return result, nil
}

// sampleColumns 通过采样命中文档推断列（与 MongoDB 模块同样的「文档 → 列」策略）。
func sampleColumns(ctx context.Context, c *esClient, physical string) ([]domain.ColumnInfo, error) {
	body := map[string]any{
		"size":  sampleDocuments,
		"query": map[string]any{"match_all": map[string]any{}},
	}
	var resp map[string]any
	if _, err := c.jsonCall(ctx, http.MethodPost, "/"+physical+"/_search", body, &resp); err != nil {
		return nil, mapQueryError(err)
	}
	var paths []string
	seen := map[string]bool{"_id": true}
	paths = append(paths, "_id")
	for _, hit := range toSlice(asMap(resp["hits"])["hits"]) {
		_, doc, err := documentJSON(asMap(hit))
		if err != nil {
			continue
		}
		flattenPaths(doc, "", 0, &paths, seen)
	}
	out := make([]domain.ColumnInfo, 0, len(paths))
	for i, path := range paths {
		out = append(out, domain.ColumnInfo{
			Position: i + 1, Name: path, DataType: "any",
			RawType: "any", Nullable: true, Comment: "采样推断",
		})
	}
	return out, nil
}

// fetchIndexDefinition 读取索引的 settings / mappings / aliases，并剔除不可回放的只读设置。
func fetchIndexDefinition(ctx context.Context, c *esClient, physical string) (map[string]any, error) {
	var settings map[string]any
	if _, err := c.jsonCall(ctx, http.MethodGet, "/"+physical+"/_settings", nil, &settings); err != nil {
		return nil, err
	}
	var mappings map[string]any
	if _, err := c.jsonCall(ctx, http.MethodGet, "/"+physical+"/_mapping", nil, &mappings); err != nil {
		return nil, err
	}
	var aliases map[string]any
	if _, err := c.jsonCall(ctx, http.MethodGet, "/"+physical+"/_alias", nil, &aliases); err != nil {
		aliases = nil // 无权限读别名时降级
	}
	def := map[string]any{}
	if s := asMap(asMap(asMap(settings[physical])["settings"])["index"]); len(s) > 0 {
		def["settings"] = replayableSettings(s)
	}
	if m := asMap(asMap(mappings[physical])["mappings"]); len(m) > 0 {
		def["mappings"] = m
	}
	if a := asMap(asMap(aliases[physical])["aliases"]); len(a) > 0 {
		def["aliases"] = a
	}
	return def, nil
}

var replayableSettingKeys = map[string]bool{
	"number_of_shards": true, "number_of_replicas": true, "refresh_interval": true,
	"analysis": true, "similarity": true, "codec": true, "hidden": true,
	"max_result_window": true, "default_pipeline": true, "final_pipeline": true,
	"sort": true, "mapping": true, "routing": true,
}

func replayableSettings(index map[string]any) map[string]any {
	out := make(map[string]any, len(index))
	for k, v := range index {
		if replayableSettingKeys[k] {
			out[k] = v
		}
	}
	return out
}

func marshalIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}
