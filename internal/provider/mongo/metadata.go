package mongo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/dbclient/dbclient/internal/domain"
)

type metadataBrowser struct {
	client *mongo.Client
}

var systemDatabases = map[string]bool{
	"admin": true, "config": true, "local": true,
}

func (b *metadataBrowser) ListDatabases(ctx context.Context) ([]domain.DatabaseInfo, error) {
	names, err := b.client.ListDatabaseNames(ctx, bson.M{})
	if err != nil {
		return nil, mapQueryError(err)
	}
	sort.Strings(names)
	out := make([]domain.DatabaseInfo, 0, len(names))
	for _, name := range names {
		out = append(out, domain.DatabaseInfo{Name: name, System: systemDatabases[name]})
	}
	return out, nil
}

// ListSchemas 在 MongoDB 中无独立 schema 层级。
func (b *metadataBrowser) ListSchemas(context.Context, string) ([]string, error) {
	return nil, nil
}

func (b *metadataBrowser) ListObjects(ctx context.Context, database, _ string) ([]domain.ObjectInfo, error) {
	if database == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.metadata", "未指定数据库")
	}
	db := b.client.Database(database)
	names, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, mapQueryError(err)
	}
	sort.Strings(names)
	out := make([]domain.ObjectInfo, 0, len(names))
	for _, name := range names {
		count, err := db.Collection(name).CountDocuments(ctx, bson.M{})
		if err != nil {
			count = 0
		}
		out = append(out, domain.ObjectInfo{
			Database: database, Name: name, Type: domain.ObjectCollection, Rows: count,
		})
	}
	return out, nil
}

func (b *metadataBrowser) Describe(ctx context.Context, ref domain.ObjectRef) (domain.TableSchema, error) {
	if ref.Database == "" || ref.Name == "" {
		return domain.TableSchema{}, domain.NewError(domain.CodeValidation, "mongo.metadata", "未指定数据库或集合")
	}
	db := b.client.Database(ref.Database)
	result := domain.TableSchema{
		Ref: domain.ObjectRef{Database: ref.Database, Name: ref.Name},
	}

	spec, err := collectionSpec(ctx, db, ref.Name)
	if err != nil {
		return result, err
	}
	if validator, ok := spec["validator"]; ok {
		if encoded, err := bson.MarshalExtJSON(validator, true, false); err == nil {
			result.Comment = truncate(string(encoded))
		}
	}
	if capped, ok := spec["capped"].(bool); ok && capped {
		result.Engine = "capped"
	}

	indexes, err := collectionIndexes(ctx, db.Collection(ref.Name))
	if err != nil {
		return result, err
	}
	result.Indexes = indexes

	columns, err := sampleColumns(ctx, db.Collection(ref.Name))
	if err != nil {
		return result, err
	}
	result.Columns = columns

	if ddl, err := (&schemaEditor{client: b.client}).ShowCreate(ctx, ref); err == nil {
		result.DDL = ddl
	}
	return result, nil
}

func collectionSpec(ctx context.Context, db *mongo.Database, name string) (map[string]any, error) {
	cursor, err := db.ListCollections(ctx, bson.M{"name": name})
	if err != nil {
		return nil, mapQueryError(err)
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc struct {
			Options map[string]any `bson:"options"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, mapQueryError(err)
		}
		if doc.Options == nil {
			return map[string]any{}, nil
		}
		return doc.Options, nil
	}
	if err := cursor.Err(); err != nil {
		return nil, mapQueryError(err)
	}
	return nil, domain.NewError(domain.CodeNotFound, "mongo.metadata", "集合 %s 不存在", name)
}

func collectionIndexes(ctx context.Context, coll *mongo.Collection) ([]domain.IndexInfo, error) {
	cursor, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, nil // 无权限时降级
	}
	defer cursor.Close(ctx)
	var out []domain.IndexInfo
	for cursor.Next(ctx) {
		_, doc, err := rawToDocument(cursor.Current)
		if err != nil {
			continue
		}
		name, _ := doc["name"].(string)
		idx := domain.IndexInfo{Name: name}
		if key, ok := asMap(doc["key"]); ok {
			keys := make([]string, 0, len(key))
			for k := range key {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			idx.Columns = keys
		}
		if unique, ok := doc["unique"].(bool); ok {
			idx.Unique = unique
		}
		idx.Primary = name == "_id_"
		out = append(out, idx)
	}
	return out, nil
}

// sampleColumns 通过采样文档推断字段，MongoDB 集合本身没有固定结构。
func sampleColumns(ctx context.Context, coll *mongo.Collection) ([]domain.ColumnInfo, error) {
	cursor, err := coll.Find(ctx, bson.M{}, options.Find().SetLimit(sampleDocuments))
	if err != nil {
		return nil, mapQueryError(err)
	}
	defer cursor.Close(ctx)

	seen := make(map[string]string)
	var order []string
	for cursor.Next(ctx) {
		_, doc, err := rawToDocument(cursor.Current)
		if err != nil {
			continue
		}
		var paths []string
		flattenPaths(doc, "", 0, &paths, make(map[string]bool))
		for _, path := range paths {
			value, _ := lookupPath(doc, path)
			typ := bsonTypeOf(value)
			if old, ok := seen[path]; ok && old != typ && typ != "null" {
				seen[path] = old + "|" + typ
				continue
			}
			if _, ok := seen[path]; !ok {
				seen[path] = typ
				order = append(order, path)
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, mapQueryError(err)
	}
	out := make([]domain.ColumnInfo, 0, len(order))
	for i, path := range order {
		out = append(out, domain.ColumnInfo{
			Position: i + 1,
			Name:     path,
			DataType: seen[path],
			RawType:  seen[path],
			Nullable: true,
			Comment:  "采样推断",
		})
	}
	return out, nil
}

func bsonTypeOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64, uint, uint32, uint64, float32, float64:
		return "number"
	case bson.ObjectID:
		return "objectId"
	case time.Time:
		return "date"
	case []byte:
		return "binData"
	case []any, bson.A:
		return "array"
	default:
		if _, ok := asMap(v); ok {
			return "object"
		}
		return strings.TrimSpace(fmt.Sprintf("%T", t))
	}
}
