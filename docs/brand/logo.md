# 栈长 Logo

原始图像通过内置 image_gen 生成，保存于 `frontend/public/logo.png`。
设计将三层堆叠与 Z 形结合，代表 Compose 栈与有序部署。

日间使用 `logo-192.png`（黑底白标），夜间使用 `logo-dark-192.png`（白底黑标）。
浏览器的 ICO、32px PNG、180px Apple Touch Icon 使用同样的两套配色。
`theme-init.js` 在首次绘制前读取已保存主题，后续主题切换由 `theme.tsx` 同步更新图标链接及应用 Logo。
ICO 包含 16、32、48px 三种尺寸；`favicon.ico` 保留为日间兼容入口。
主题版本由浏览器将同一原图按对比度与反色规则导出，保持几何形状一致。

## 原始生成提示词

Use case: logo-brand. Asset type: production app logo and browser favicon for 栈长 (Zhan), a self-hosted Docker Compose and Git deployment manager. Create ONE original minimalist geometric emblem: three bold horizontal stacked layers, subtly forming a stylized Z through confident negative space, suggesting orderly infrastructure and deployment stacks. Single compact centered symbol, no typography, no letters as text, no wordmark. Solid near-white mark on a solid charcoal #212121 rounded-square tile, square canvas, generous but modest 15% inner safe area. Flat crisp vector-like geometry, simple thick forms legible at 16px and 32px, restrained premium developer-tool identity matching neutral light/dark UI. No gradients, no shadows, no texture, no 3D, no mockup, no multiple proposals. The tile should fill the canvas; an attractive standalone icon, not a presentation sheet. Original design, avoid existing brand symbols.
