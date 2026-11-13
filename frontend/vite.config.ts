import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";

// 两套前端模板编译入口：
//   --mode gui (默认) -> dist/     + runtime/gui.ts (Wails IPC)，供 cmd/db-fly-gui embed
//   --mode web        -> dist-web/ + runtime/web.ts (HTTP RPC)，供 internal/webui embed
export default defineConfig(({ mode }) => {
  const web = mode === "web";
  const srcDir = fileURLToPath(new URL("./src/", import.meta.url)).replace(/\\/g, "/");
  const runtimeFile = srcDir + (web ? "runtime/web.ts" : "runtime/gui.ts");
  const apiTarget = process.env.VITE_API_TARGET || "http://127.0.0.1:8080";

  return {
    plugins: [vue()],
    resolve: {
      // 精确匹配必须排在 @/ 通配之前：编译期物理替换运行时实现。
      alias: [
        { find: /^@\/runtime$/, replacement: runtimeFile },
        { find: /^@\//, replacement: srcDir },
      ],
    },
    build: {
      outDir: web ? "dist-web" : "dist",
      // Web 产物目录由 build/web.ps1 在构建前清理：部分 Windows 环境的
      // 删除保护会拦截 Vite 的 emptyOutDir，导致重复构建失败。
      emptyOutDir: !web,
      target: "esnext",
    },
    server: {
      port: 34115,
      strictPort: true,
      // Web 开发模式由 Vite 提供 HMR，API / 登录请求代理到 Go 进程。
      proxy: web
        ? {
            "/api": { target: apiTarget, changeOrigin: true },
            "/login": { target: apiTarget, changeOrigin: true },
            "/logout": { target: apiTarget, changeOrigin: true },
          }
        : undefined,
    },
  };
});
