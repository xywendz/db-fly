// 两种编译模板共用的运行时契约。
// Vite 在编译期把 @/runtime 物理替换为 runtime/gui.ts 或 runtime/web.ts。

export type RuntimeMode = "gui" | "web";

// 与 Wails 绑定一致的门面形状：方法名 → 返回 Promise 的函数。
export type RawBridge = Record<string, (...args: any[]) => Promise<any>>;

// 导入文件选择器统一返回「后端可读取的路径 + 展示用文件名」。
// GUI 模式是本机绝对路径；Web 模式是上传后服务端临时目录中的路径。
export interface PickedFile {
  path: string;
  name: string;
}

// B/S 模式当前登录用户；GUI 模式恒为 null。
export interface AuthUser {
  id: string;
  username: string;
  created_at: string;
}
