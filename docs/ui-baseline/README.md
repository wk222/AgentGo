# 前端基线（M0，2026-10-17）

每个里程碑开始前、结束后各跑一遍，数字不得变差（体积允许下降）。

```powershell
cd frontend
npm run typecheck   # tsc --noEmit，必须零输出
npm test            # runtime-client 单测，2 项通过
npx vite build      # 必须成功
```

| 项 | M0 值 |
|---|---|
| `tsc --noEmit` | 0 错误 |
| `npm test` | 2/2 通过 |
| `dist/` 总字节 | 15,822,214 |
| `dist/assets/index-*.js` | 4,948,555 |
| `!important`（`src/styles`） | 29 |
| `alert(/confirm(/prompt(`（`src`） | 约 51 处 |
| 超过 800 行的 tsx/ts | App.tsx 869、panels/workspace/index.tsx 869、ChatPanel.tsx 785、panels/index.tsx 785 |

截图（浏览器预览 mock 模式，`npx vite --port 5199`）：`m0-code-1200x760.png`。

## M7 收尾值（同一套命令）

| 项 | M0 | M7 |
|---|---|---|
| `tsc --noEmit` | 0 | 0 |
| `npm test` | 2/2 | 8/8（新增 layout 6 项） |
| `dist/` 总字节 | 15,822,214 | 15,826,284（+4 KB，新增 shell/store/notify 代码；删 naive-ui 后主包 4,948,555 → 4,946,072） |
| `!important` | 29 | 5 |
| 原生 `alert/confirm/prompt` | 约 51 | 0（唯一命中是 `notify.ts` 里 `confirmDialog` 自己的函数名） |
| 超 800 行文件 | 4 个 | 1 个（`features/workspace/index.tsx` 844） |
| `App.tsx` | 869 行 | 42 行 |
