// B/S 模式运行时：HTTP JSON-RPC 门面 + 用户会话 + 浏览器文件上传。
// 仅在 `npm run build:web` / `npm run dev:web` (--mode web) 时被打进产物，
// 因此 Web 产物完全不包含 @wailsio/runtime。
import type { AuthUser, PickedFile, RawBridge, RuntimeMode } from "./types";

export const runtimeMode: RuntimeMode = "web";

function redirectToLogin(): void {
  const next = location.pathname + location.search + location.hash;
  location.assign(`/login?next=${encodeURIComponent(next)}`);
}

async function invoke(method: string, args: any[]): Promise<any> {
  let response: Response;
  try {
    response = await fetch(`/api/rpc/${encodeURIComponent(method)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ args }),
    });
  } catch (e: any) {
    throw new Error(`无法连接 db-fly 服务：${e?.message ?? String(e)}`);
  }
  if (response.status === 401) {
    redirectToLogin();
    throw new Error("登录已失效，请重新登录");
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error: any = new Error(body?.message || `请求失败 (HTTP ${response.status})`);
    error.code = body?.code;
    throw error;
  }
  return body.result;
}

export async function currentUser(): Promise<AuthUser | null> {
  try {
    const res = await fetch("/api/auth/me", { credentials: "same-origin" });
    if (!res.ok) return null;
    return (await res.json()) as AuthUser;
  } catch {
    return null;
  }
}

export async function logout(): Promise<void> {
  try {
    await fetch("/api/auth/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: "{}",
    });
  } finally {
    location.assign("/login");
  }
}

// Proxy 让 api/client.ts 的 43 个方法调用保持与 Wails 绑定完全相同的形状。
export const raw: RawBridge = new Proxy({} as RawBridge, {
  get(_target, prop) {
    if (typeof prop !== "string") return undefined;
    return (...args: any[]) => invoke(prop, args);
  },
});

function selectFile(): Promise<File | null> {
  return new Promise((resolve) => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".csv,.tsv,.txt,.sql,.xlsx,.xlsm,.json,.jsonl,.ndjson";
    input.style.display = "none";
    document.body.appendChild(input);
    let settled = false;
    const finish = (file: File | null) => {
      if (settled) return;
      settled = true;
      input.remove();
      resolve(file);
    };
    input.addEventListener("change", () => finish(input.files?.[0] ?? null));
    input.addEventListener("cancel", () => finish(null));
    input.click();
  });
}

export async function pickImportFile(): Promise<PickedFile | null> {
  const file = await selectFile();
  if (!file) return null;
  const form = new FormData();
  form.append("file", file);
  let response: Response;
  try {
    response = await fetch("/api/upload", {
      method: "POST",
      credentials: "same-origin",
      body: form,
    });
  } catch (e: any) {
    throw new Error(`文件上传失败：${e?.message ?? String(e)}`);
  }
  if (response.status === 401) {
    redirectToLogin();
    throw new Error("登录已失效，请重新登录");
  }
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(body?.message || `文件上传失败 (HTTP ${response.status})`);
  }
  return { path: body.path, name: body.name || file.name };
}
