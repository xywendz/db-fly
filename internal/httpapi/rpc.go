package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/db-fly/db-fly/internal/bridge"
	"github.com/db-fly/db-fly/internal/domain"
)

// allowedMethods 是 B/S 模式对外的接口契约：与 frontend/src/api/client.ts 的 43 个调用一一对应。
// 白名单而非全量反射导出，可避免把构造器、辅助方法意外暴露为 HTTP 端点。
var allowedMethods = map[string]struct{}{
	// 连接管理
	"ListConnections": {}, "SaveConnection": {}, "RemoveConnection": {},
	"CloseConnection": {}, "OpenConnections": {}, "TestConnection": {},
	"TestSavedConnection": {}, "ListKinds": {}, "KindCapabilities": {},
	"ValidateConnection": {},
	// 元数据浏览
	"ServerInfo": {}, "ConnectionCapabilities": {}, "ListDatabases": {},
	"ListSchemas": {}, "ListObjects": {}, "DescribeObject": {},
	// 查询执行
	"ExecuteQuery": {}, "ExplainQuery": {}, "ExecStatement": {}, "ExecuteScript": {},
	// 结构 / 建表
	"PreviewCreateDatabase": {}, "PreviewAlterDatabase": {}, "PreviewCreateTable": {},
	"PreviewAlterTable": {}, "PreviewDropObject": {}, "PreviewRenameTable": {},
	"ShowCreateObject": {}, "PreviewTableAction": {}, "PreviewCopyTable": {},
	"ApplyScript": {}, "ExportDatabase": {},
	// 数据编辑
	"PreviewDataEdit": {}, "ApplyDataEdit": {}, "TableKeyColumns": {},
	// 导入
	"ImportFile": {},
	// 历史 / 收藏 / 信息
	"Info": {}, "ListHistory": {}, "ClearHistory": {}, "ListSavedQueries": {},
	"SaveSavedQuery": {}, "DeleteSavedQuery": {}, "ListPreferences": {},
	"SavePreferences": {},
}

type rpcRequest struct {
	Args []json.RawMessage `json:"args"`
}

type rpcResponse struct {
	Result any `json:"result"`
}

// handleRPC 把一个 POST 请求转换成对 *bridge.Bridge 导出方法的一次调用。
// 每次请求构造独立的 Bridge（只持有 *app.App 与运行时字符串，成本极低），
// 因此并发请求之间不存在共享可变状态。
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("method")
	if _, ok := allowedMethods[name]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "未知的 API 方法: "+name)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRPCBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "validation", "请求体过大或读取失败: "+err.Error())
		return
	}
	var req rpcRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, "validation", "请求参数格式错误: "+err.Error())
			return
		}
	}

	rt, ok := runtimeFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	// 每次请求使用当前用户的业务内核，并把事件绑定到该用户的 SSE 连接。
	b := bridge.WithEmitter(
		bridge.New(rt.App, s.version, rt.DataDir, rt.SecretsName),
		userEmitter{hub: s.events, userID: rt.User.ID},
	)
	method := reflect.ValueOf(b).MethodByName(name)
	if !method.IsValid() {
		writeError(w, http.StatusInternalServerError, "internal", "方法未实现: "+name)
		return
	}
	mt := method.Type()
	if len(req.Args) > mt.NumIn() {
		writeError(w, http.StatusBadRequest, "validation",
			fmt.Sprintf("方法 %s 最多接受 %d 个参数，收到 %d 个", name, mt.NumIn(), len(req.Args)))
		return
	}
	in := make([]reflect.Value, mt.NumIn())
	for i := range in {
		arg := reflect.New(mt.In(i))
		if i < len(req.Args) && string(req.Args[i]) != "null" {
			if err := json.Unmarshal(req.Args[i], arg.Interface()); err != nil {
				writeError(w, http.StatusBadRequest, "validation",
					fmt.Sprintf("方法 %s 第 %d 个参数解析失败: %v", name, i+1, err))
				return
			}
		}
		in[i] = arg.Elem()
	}
	// 导入请求只能引用本进程上传目录内的文件，禁止读取服务端任意路径。
	if name == "ImportFile" {
		if err := secureImportPath(in[0], rt.UploadDir); err != nil {
			writeError(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
	}

	out := method.Call(in)
	last := out[len(out)-1]
	if !last.IsNil() {
		err, ok := last.Interface().(error)
		if !ok {
			writeError(w, http.StatusInternalServerError, "internal", "方法返回值不是 error")
			return
		}
		status, code, message := describeError(err)
		s.logf("httpapi: %s 失败: %s", name, message)
		writeError(w, status, code, message)
		return
	}
	var result any
	if len(out) > 1 {
		result = out[0].Interface()
	}
	writeJSON(w, http.StatusOK, rpcResponse{Result: result})
}

// describeError 把领域错误映射为 HTTP 状态码与前端可展示的结构化信息。
func describeError(err error) (int, string, string) {
	var de *domain.Error
	if errors.As(err, &de) {
		return http.StatusBadRequest, string(de.Code), domain.UserMessage(err)
	}
	return http.StatusInternalServerError, "internal", err.Error()
}

// secureImportPath 校验并规范化 ImportFile 的文件路径，
// 只允许引用当前用户上传目录内的文件。
func secureImportPath(v reflect.Value, uploadDir string) error {
	req, ok := v.Interface().(bridge.ImportRequest)
	if !ok || !v.CanSet() {
		return errors.New("导入参数类型异常")
	}
	if strings.TrimSpace(req.Path) == "" {
		return errors.New("未指定导入文件，请先上传")
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil {
		return errors.New("导入文件路径无效")
	}
	rel, err := filepath.Rel(uploadDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return errors.New("导入文件必须来自上传接口")
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return errors.New("导入文件不存在或已被清理，请重新上传")
	}
	req.Path = abs
	v.Set(reflect.ValueOf(req))
	return nil
}
