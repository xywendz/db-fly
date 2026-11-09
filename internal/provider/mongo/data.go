package mongo

import (
	"context"
	"encoding/json"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/db-fly/db-fly/internal/domain"
)

type dataEditor struct {
	client *mongo.Client
}

func (e *dataEditor) collection(req domain.EditRequest) (*mongo.Collection, error) {
	if req.Ref.Database == "" || req.Ref.Name == "" {
		return nil, domain.NewError(domain.CodeValidation, "mongo.edit", "未指定数据库或集合")
	}
	return e.client.Database(req.Ref.Database).Collection(req.Ref.Name), nil
}

func editFilter(edit domain.RowEdit) (bson.M, error) {
	if len(edit.Key) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "mongo.edit",
			"更新或删除必须提供 _id 等唯一键值")
	}
	filter := bson.M{}
	for column, value := range edit.Key {
		filter[column] = resolveKeyValue(column, value)
	}
	return filter, nil
}

func previewText(v any) (string, error) {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", domain.WrapError(domain.CodeValidation, "mongo.edit", err, "预览序列化失败")
	}
	return string(encoded), nil
}

func (e *dataEditor) Preview(ctx context.Context, req domain.EditRequest) ([]string, error) {
	if len(req.Edits) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "mongo.edit", "没有待提交的变更")
	}
	out := make([]string, 0, len(req.Edits))
	for i, edit := range req.Edits {
		var command map[string]any
		switch edit.Kind {
		case domain.EditUpdate:
			filter, err := editFilter(edit)
			if err != nil {
				return nil, err
			}
			set := bson.M{}
			for column, value := range edit.Values {
				if column == "_id" {
					continue
				}
				if value == nil {
					set[column] = nil
				} else {
					set[column] = parseScalar(*value)
				}
			}
			command = map[string]any{
				"collection": req.Ref.Name,
				"updateOne":  []any{filter, bson.M{"$set": set}},
			}
		case domain.EditDelete:
			filter, err := editFilter(edit)
			if err != nil {
				return nil, err
			}
			command = map[string]any{"collection": req.Ref.Name, "deleteOne": filter}
		case domain.EditInsert:
			doc := bson.M{}
			for column, value := range edit.Values {
				if value == nil {
					doc[column] = nil
				} else {
					doc[column] = parseScalar(*value)
				}
			}
			command = map[string]any{"collection": req.Ref.Name, "insertOne": doc}
		default:
			return nil, domain.NewError(domain.CodeUnsupported, "mongo.edit", "不支持的编辑类型 %q", edit.Kind)
		}
		text, err := previewText(command)
		if err != nil {
			return nil, err
		}
		out = append(out, text)
		_ = i
	}
	return out, nil
}

func (e *dataEditor) Apply(ctx context.Context, req domain.EditRequest) (domain.EditResult, error) {
	coll, err := e.collection(req)
	if err != nil {
		return domain.EditResult{}, err
	}
	previews, err := e.Preview(ctx, req)
	if err != nil {
		return domain.EditResult{}, err
	}
	result := domain.EditResult{Statements: previews}
	for i, edit := range req.Edits {
		switch edit.Kind {
		case domain.EditUpdate:
			filter, err := editFilter(edit)
			if err != nil {
				return result, err
			}
			set := bson.M{}
			for column, value := range edit.Values {
				if column == "_id" {
					continue
				}
				if value == nil {
					set[column] = nil
				} else {
					set[column] = parseScalar(*value)
				}
			}
			if len(set) == 0 {
				return result, domain.NewError(domain.CodeValidation, "mongo.edit", "更新内容为空")
			}
			_, err = coll.UpdateOne(ctx, filter, bson.M{"$set": set})
		case domain.EditDelete:
			filter, err := editFilter(edit)
			if err != nil {
				return result, err
			}
			_, err = coll.DeleteOne(ctx, filter)
		case domain.EditInsert:
			doc := bson.M{}
			for column, value := range edit.Values {
				if value == nil {
					doc[column] = nil
				} else {
					doc[column] = parseScalar(*value)
				}
			}
			_, err = coll.InsertOne(ctx, doc)
		default:
			return result, domain.NewError(domain.CodeUnsupported, "mongo.edit", "不支持的编辑类型 %q", edit.Kind)
		}
		if err != nil {
			return result, mapQueryError(err)
		}
		_ = i
		result.Applied++
	}
	return result, nil
}
