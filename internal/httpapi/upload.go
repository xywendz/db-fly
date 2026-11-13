package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 前端 pickImportFile 上传的文件需保留扩展名：app.detectImportFormat 依赖它推断格式。
var uploadExtensions = map[string]struct{}{
	".csv": {}, ".tsv": {}, ".txt": {}, ".sql": {},
	".xlsx": {}, ".xlsm": {}, ".json": {}, ".jsonl": {}, ".ndjson": {},
}

type uploadResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleUpload 接收浏览器上传的导入文件，返回仅本进程可见的临时绝对路径。
// 该路径随后作为 ImportRequest.Path 传给 ImportFile，并由 RPC 层再次校验归属。
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	rt, ok := runtimeFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "validation", "文件上传解析失败: "+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", "缺少文件字段 file")
		return
	}
	defer file.Close()

	// 浏览器一般只给文件名，这里仍做路径清洗，避免跨平台分隔符造成目录穿越。
	name := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	ext := strings.ToLower(path.Ext(name))
	if _, ok := uploadExtensions[ext]; !ok {
		writeError(w, http.StatusBadRequest, "validation", "不支持的导入文件类型: "+ext)
		return
	}
	id, err := randomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "生成文件名失败")
		return
	}
	dst := filepath.Join(rt.UploadDir, id+ext)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "保存上传文件失败: "+err.Error())
		return
	}
	defer out.Close()
	size, err := io.Copy(out, file)
	if err != nil {
		_ = os.Remove(dst)
		writeError(w, http.StatusBadRequest, "validation", "写入上传文件失败: "+err.Error())
		return
	}
	if size == 0 {
		_ = os.Remove(dst)
		writeError(w, http.StatusBadRequest, "validation", "上传文件为空")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(uploadResponse{Path: dst, Name: name, Size: size})
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
