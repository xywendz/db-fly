// 统一导入由 wails 生成的绑定（JS + JSDoc），并屏蔽其类型检查。
// 真正的类型安全由 ./client.ts 基于 src/types/domain.ts 提供。
// @ts-nocheck
import * as Bridge from "../bindings/github.com/db-fly/db-fly/internal/bridge/bridge.js";

export const raw = Bridge as Record<string, (...args: any[]) => Promise<any>>;
