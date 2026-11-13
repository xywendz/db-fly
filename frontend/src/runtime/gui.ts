// GUI 本地模式运行时：Wails 数字 ID IPC + 原生文件对话框。
// 仅在 `npm run build` / `npm run dev` (--mode gui) 时被打进产物。
import { Dialogs } from "@wailsio/runtime";
import { raw as wailsRaw } from "../api/bridge";
import type { AuthUser, PickedFile, RawBridge, RuntimeMode } from "./types";

export const runtimeMode: RuntimeMode = "gui";
export const raw: RawBridge = wailsRaw;

// 本地 GUI 由操作系统会话管理，不存在 Web 账号概念。
export async function currentUser(): Promise<AuthUser | null> {
  return null;
}

export async function logout(): Promise<void> {
  return;
}

export async function pickImportFile(): Promise<PickedFile | null> {
  const filters = [
    {
      DisplayName: "支持的导入文件",
      Pattern: "*.csv;*.tsv;*.txt;*.sql;*.xlsx;*.xlsm;*.json;*.jsonl;*.ndjson",
    },
  ];
  let result: string | string[];
  try {
    result = await Dialogs.OpenFile({
      Title: "选择要导入的文件",
      Filters: filters,
      CanChooseFiles: true,
    });
  } catch {
    return null;
  }
  if (!result) return null;
  const p = (Array.isArray(result) ? result[0] : result) || "";
  if (!p) return null;
  return { path: p, name: p.split(/[\\/]/).pop() || p };
}
