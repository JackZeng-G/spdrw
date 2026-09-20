# 前端测试

两层验证, 各有分工:

## 1. Node 契约测试(快, 可在 CI 跑)

```bash
node --test "frontend/test/*.test.mjs"
```

用 `harness.mjs`(极简 DOM + Node vm)加载**真实的** `dist/app.js`, 断言的是
**JS↔Go 绑定契约**: 方法名、参数形态(例如 `WPSet` 必须传数字数组而不是 `[]byte`
能吃的东西)、返回值用法, 以及渲染结果的关键文案。本项目两次线上的"点了没反应"
都是这一层能拦下来的问题(返回值超过 2 个被 Wails 序列化成 null)。

## 2. 真实浏览器冒烟(`smoke.html`)

Node 夹具不是真 DOM: `classList.toggle(cls, force)`、`querySelector`、CSS 布局
都必须用真浏览器验一遍。用法(平台自带的 agent 浏览器):

1. 打开 `file://<repo>/frontend/test/smoke.html`
2. 页面会按真实用户顺序点一遍(选设备→读 dump→看 hex/信息→查保护状态→
   进编辑器→载入→改字段→写入面板→干跑提示), 结果写到 `window.__SMOKE`
3. 读 `window.__SMOKE.steps`(全部 `ok`)与 `window.__SMOKE.jsErrors`(必须为空)

`smoke.html` 里的 `window.go.app.App` 是桩, 覆盖 App 当前全部绑定方法;
**新增绑定方法后要同步补桩**, 否则冒烟页会报"找不到方法"。
