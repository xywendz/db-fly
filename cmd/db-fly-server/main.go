// db-fly-server 是 B/S 模式的独立编译入口：Vue 前端由 internal/webui 嵌入本进程，
// 多用户账号与数据隔离由 internal/auth 提供，业务请求经 internal/httpapi 分发。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/db-fly/db-fly/internal/auth"
	"github.com/db-fly/db-fly/internal/bridge"
	"github.com/db-fly/db-fly/internal/httpapi"
	"github.com/db-fly/db-fly/internal/platform/logging"
	"github.com/db-fly/db-fly/internal/webui"
	// 注册全部数据库业务模块（各自 init() 自注册到 provider 注册中心）。
	_ "github.com/db-fly/db-fly/internal/provider/all"
)

const defaultAddr = "127.0.0.1:8080"

func main() {
	addr := flag.String("addr", env("DB_FLY_WEB_ADDR", defaultAddr), "HTTP 监听地址(host:port)，例如 127.0.0.1:8080 或 :8080；与 -port 同时使用时取其主机部分")
	port := flag.String("port", env("DB_FLY_WEB_PORT", ""), "HTTP 监听端口（可选）；设置后覆盖 addr 中的端口部分，例如 8080")
	dataDir := flag.String("data-dir", env("DB_FLY_DATA_DIR", ""), "账号表与各用户数据根目录（默认 ~/.db-fly）")
	signup := flag.Bool("allow-signup", envBool("DB_FLY_ALLOW_SIGNUP", true), "是否开放用户注册")
	invite := flag.String("invite", env("DB_FLY_WEB_TOKEN", ""), "注册邀请码（可选；公网建议设置）")
	uploadDir := flag.String("upload-dir", env("DB_FLY_UPLOAD_DIR", ""), "导入文件暂存根目录（默认各用户目录内 uploads/）")
	logBase := flag.String("log-dir", env("DB_FLY_LOG_DIR", ""), "日志根目录（默认可执行文件所在目录；实际写入其中 logs/ 子目录）")
	flag.Parse()

	listenAddr := resolveAddr(*addr, *port)

	// 初始化日志：在「运行目录/logs」下输出标准日志与数据库操作审计日志。
	loggingBase := *logBase
	if loggingBase == "" {
		if exe, e := os.Executable(); e == nil {
			loggingBase = filepath.Dir(exe)
		}
	}
	auditLogger, closeLog, logErr := logging.Setup(loggingBase)
	if logErr != nil {
		log.Printf("初始化日志失败: %v", logErr)
		return
	}
	defer closeLog()
	log.Printf("日志目录: %s/logs", loggingBase)

	// 对外监听且未指定邀请码时自动生成，避免公网被任意注册。
	if !isLoopback(listenAddr) && *invite == "" {
		generated, err := randomToken()
		if err != nil {
			log.Printf("生成注册邀请码失败: %v", err)
			return
		}
		*invite = generated
		log.Println("=========================================================")
		log.Printf("db-fly 对外监听 %s，已自动生成注册邀请码: %s", *addr, generated)
		log.Println("注册第一个账号时需要填写；可用 DB_FLY_WEB_TOKEN 固定")
		log.Println("=========================================================")
	}

	mgr, err := auth.New(auth.Options{
		DataDir:     *dataDir,
		UploadDir:   *uploadDir,
		AllowSignup: *signup,
		SignupCode:  *invite,
		SessionTTL:  7 * 24 * time.Hour,
		Audit:       auditLogger,
	})
	if err != nil {
		log.Printf("初始化多用户管理器失败: %v", err)
		return
	}
	api, err := httpapi.New(mgr, httpapi.Options{
		Version: bridge.Version,
		Static:  webui.FS(),
	})
	if err != nil {
		log.Printf("初始化 HTTP 服务失败: %v", err)
		_ = mgr.Close()
		return
	}

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// 不设置 WriteTimeout：SQL 查询与 SSE 长连接可能持续较久。
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("HTTP 服务关闭异常: %v", err)
		}
	}()

	log.Printf("db-fly Web 已启动: http://%s (版本 %s, 数据目录 %s, 注册 %s)",
		listenAddr, bridge.Version, mgr.UsersDir(), map[bool]string{true: "开放", false: "关闭"}[*signup])
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("HTTP 服务异常: %v", err)
	}
	_ = api.Close()
	_ = mgr.Close()
}

// resolveAddr 合并 -addr 与可选的 -port：port 非空时只取其端口部分覆盖到
// addr 的端口上（addr 仅作主机来源；":8080" 形式的空主机保留为全接口监听）。
func resolveAddr(addr, port string) string {
	if port == "" {
		return addr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// addr 未带端口，整体当作主机名使用。
		host = addr
	}
	return net.JoinHostPort(host, port)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
}

// isLoopback 判断监听地址是否仅限本机；无法解析时按"对外"处理以启用邀请码。
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
