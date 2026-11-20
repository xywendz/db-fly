package selfupdate

import "github.com/wailsapp/wails/v3/pkg/updater"

// builtinWindowCSS is appended to Wails' built-in updater template. The
// framework resizes the compact states (checking / up-to-date / error) to
// 348x161, so those states get a tighter rhythm and a scrollable error box
// instead of clipping the long provider error text.
const builtinWindowCSS = `
:root {
  --bg: #f5f6f8;
  --surface: #ffffff;
  --surface-2: #eef1f5;
  --border: #dfe3ea;
  --accent: #2563eb;
  --accent-dim: rgba(37, 99, 235, .14);
  --radius: 8px;
  --radius-sm: 5px;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #17191d;
    --surface: #20242b;
    --surface-2: #2a2f38;
    --border: #363c47;
    --accent: #5b9cff;
    --accent-dim: rgba(91, 156, 255, .16);
  }
}
body { font-size: 12.5px; }
.u { position: relative; padding: 14px 16px; gap: 10px; }
.u__hero { align-items: center; gap: 10px; }
.u__icon { width: 38px; height: 38px; border-radius: 9px; font-size: 21px; }
.u__head { gap: 3px; }
.u__title { font-size: 15px; }
.u__subtitle { gap: 6px; font-size: 11.5px; }
.u__notes { padding: 10px 12px; font-size: 12px; }
.u__progress-box { padding: 9px 12px; gap: 5px; }
.u__spinner { min-height: 48px; gap: 10px; font-size: 12px; }
.u__error {
  max-height: 88px;
  overflow-y: auto;
  overflow-wrap: anywhere;
  line-height: 1.5;
  padding: 8px 10px;
  font-size: 11.5px;
}
.u__error::-webkit-scrollbar { width: 8px; }
.u__error::-webkit-scrollbar-thumb { background: var(--border); border-radius: 4px; }
.u__footer { padding-top: 10px; border-top: 1px solid var(--border); }
.u__btn { padding: 6px 13px; border-radius: var(--radius-sm); }
.u__btn--ghost { padding: 6px 8px; }

/* Compact states keep the framework's 348x161 card readable. */
.u[data-state="checking"],
.u[data-state="up-to-date"],
.u[data-state="error"] { padding: 10px 12px; gap: 7px; }
.u[data-state="up-to-date"] .u__icon { width: 34px; height: 34px; }
.u[data-state="up-to-date"] .u__footer { padding-top: 8px; }

/* Error state: the framework hard-resizes the window to 348x161 while the
   provider error text can be very long. Pin the footer to the bottom and give
   the scrollable error box a fixed max height that stops above it, so Close
   and Try Again are always visible. */
.u[data-state="error"] { padding-bottom: 50px; gap: 6px; }
.u[data-state="error"] .u__hero { flex: 0 0 auto; gap: 8px; }
.u[data-state="error"] .u__icon { width: 24px; height: 24px; border-radius: 6px; font-size: 13px; }
.u[data-state="error"] .u__title { font-size: 13px; }
.u[data-state="error"] .u__error {
  flex: 1 1 auto;
  min-height: 0;
  max-height: none;
  margin: 0;
  padding: 5px 7px;
  font-size: 10.5px;
  line-height: 1.35;
}
.u[data-state="error"] .u__footer {
  position: absolute;
  left: 0; right: 0; bottom: 0;
  padding: 6px 12px 8px;
  border-top: 1px solid var(--border);
  background: var(--bg);
  justify-content: flex-end;
  gap: 6px;
}
.u[data-state="error"] .u__btn { padding: 4px 9px; font-size: 11px; }
`

func builtinWindow() *updater.BuiltinWindow {
	return &updater.BuiltinWindow{
		CSS: builtinWindowCSS,
		Options: updater.WindowOptions{
			Title: "db-fly 软件更新",
		},
	}
}
