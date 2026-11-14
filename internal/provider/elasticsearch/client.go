package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/db-fly/db-fly/internal/domain"
)

const (
	userAgent        = "db-fly"
	maxResponseBytes = 64 << 20
)

// esClient 是零第三方依赖的 Elasticsearch REST 客户端。
// 只持有配置与连接池，不缓存任何集群状态之外的可变数据。
type esClient struct {
	base                string
	http                *http.Client
	authHeader          string
	apiVersion          string
	version             string
	composableTemplates bool
	cfg                 domain.ConnectionConfig
}

// esHTTPError 保留 ES 的 error.type / error.reason，便于映射成领域错误码。
type esHTTPError struct {
	Status int
	Type   string
	Reason string
	Index  string
}

func (e *esHTTPError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("Elasticsearch HTTP %d", e.Status)
	}
	return fmt.Sprintf("Elasticsearch HTTP %d [%s]: %s", e.Status, e.Type, e.Reason)
}

func newClient(cfg domain.ConnectionConfig) (*esClient, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, domain.NewError(domain.CodeValidation, "es.config", "主机地址不能为空")
	}
	port := cfg.Port
	if port <= 0 {
		port = domain.DefaultPort(domain.KindElasticsearch)
	}
	scheme := "http"
	switch strings.ToLower(strings.TrimSpace(cfg.TLSMode)) {
	case "", "false", "disabled", "preferred":
	case "true", "skip-verify":
		scheme = "https"
	default:
		return nil, domain.NewError(domain.CodeValidation, "es.config",
			"不支持的 TLS 模式 %q（可用 false/true/skip-verify）", cfg.TLSMode)
	}
	if v := strings.ToLower(strings.TrimSpace(cfg.Params["scheme"])); v == "http" || v == "https" {
		scheme = v
	}
	prefix := strings.TrimSpace(cfg.Params["pathPrefix"])
	if prefix != "" {
		if !strings.HasPrefix(prefix, "/") {
			prefix = "/" + prefix
		}
		prefix = strings.TrimRight(prefix, "/")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	if strings.EqualFold(strings.TrimSpace(cfg.TLSMode), "skip-verify") {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // 用户显式选择跳过证书校验
	}
	c := &esClient{
		base:                fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(cfg.Host, strconv.Itoa(port)), prefix),
		http:                &http.Client{Transport: transport, Timeout: cfg.EffectiveTimeout()},
		apiVersion:          strings.TrimSpace(cfg.Params["apiVersion"]),
		composableTemplates: true,
		cfg:                 cfg,
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Params["auth"])) {
	case "", "basic":
		if cfg.User != "" || cfg.Password != "" {
			token := base64.StdEncoding.EncodeToString([]byte(cfg.User + ":" + cfg.Password))
			c.authHeader = "Basic " + token
		}
	case "apikey":
		if cfg.Password == "" {
			return nil, domain.NewError(domain.CodeValidation, "es.config",
				"ApiKey 认证需要在密码栏填写 API Key")
		}
		c.authHeader = "ApiKey " + cfg.Password
	case "none":
	default:
		return nil, domain.NewError(domain.CodeValidation, "es.config",
			"不支持的认证方式 %q（可用 basic/apikey/none）", cfg.Params["auth"])
	}
	return c, nil
}

func (c *esClient) Close() { c.http.CloseIdleConnections() }

// setVersion 记录服务端版本，决定索引模板使用组合模板还是 7.7 及以前的旧模板。
func (c *esClient) setVersion(v string) {
	c.version = v
	c.composableTemplates = versionAtLeast(v, 7, 8)
}

func versionAtLeast(v string, major, minor int) bool {
	parts := strings.Split(strings.TrimSpace(v), ".")
	get := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return 0
		}
		return n
	}
	m, n := get(0), get(1)
	if m == 0 {
		return true // 无法解析时按新版本处理（兼容 OpenSearch 等发行版）
	}
	return m > major || (m == major && n >= minor)
}

func (c *esClient) templatePath(db string) string {
	if c.composableTemplates {
		return "/_index_template/" + templatePrefix + db
	}
	return "/_template/" + templatePrefix + db
}

func (c *esClient) url(path string) string {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.base + path
}

// do 发起一次 REST 调用；HTTP 4xx/5xx 转成 esHTTPError，响应体保留供上层诊断。
func (c *esClient) do(ctx context.Context, method, path string, body []byte, contentType string) ([]byte, int, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), reader)
	if err != nil {
		return nil, 0, domain.WrapError(domain.CodeValidation, "es.client", err, "构造请求失败")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.apiVersion != "" {
		req.Header.Set("Elastic-Api-Version", c.apiVersion)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return raw, resp.StatusCode, parseESError(raw, resp.StatusCode)
	}
	return raw, resp.StatusCode, nil
}

func parseESError(raw []byte, status int) error {
	var payload struct {
		Error struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
			Index  string `json:"index"`
		} `json:"error"`
		Status int `json:"status"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && (payload.Error.Reason != "" || payload.Error.Type != "") {
		return &esHTTPError{
			Status: status, Type: payload.Error.Type,
			Reason: payload.Error.Reason, Index: payload.Error.Index,
		}
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return &esHTTPError{Status: status, Reason: text}
}

// jsonCall 发送 JSON 请求并按需解码响应。
func (c *esClient) jsonCall(ctx context.Context, method, path string, body, out any) (int, error) {
	var data []byte
	contentType := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, domain.WrapError(domain.CodeValidation, "es.client", err, "请求体序列化失败")
		}
		data, contentType = encoded, "application/json"
	}
	raw, status, err := c.do(ctx, method, path, data, contentType)
	if err != nil {
		return status, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, domain.WrapError(domain.CodeQuery, "es.client", err, "响应不是合法 JSON")
		}
	}
	return status, nil
}

// ndjsonCall 发送 NDJSON 请求体（_bulk 使用）。
func (c *esClient) ndjsonCall(ctx context.Context, method, path string, body []byte, out any) (int, error) {
	raw, status, err := c.do(ctx, method, path, body, "application/x-ndjson")
	if err != nil {
		return status, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, domain.WrapError(domain.CodeQuery, "es.client", err, "响应不是合法 JSON")
		}
	}
	return status, nil
}

// exists 用 HEAD 判断索引是否存在，404 视为 false。
func (c *esClient) exists(ctx context.Context, path string) (bool, error) {
	_, status, err := c.do(ctx, http.MethodHead, path, nil, "")
	if err != nil {
		var he *esHTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return status >= 200 && status < 300, nil
}

func (c *esClient) timeout() time.Duration { return c.http.Timeout }
