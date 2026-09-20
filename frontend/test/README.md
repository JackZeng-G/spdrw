# 前端测试

三层验证, 各有分工。**新增绑定方法或页面元素后, 先跑第 1 层**——它会直接告诉你漏了什么。

## 1. Node 契约与漂移守卫(秒级, CI 可跑)

```bash
node --test "frontend/test/*.test.mjs"
```

- `page.test.mjs`: **漂移守卫**。机械校验
  ① `app.js` 里 `$("x")` 用到的每个 id 都存在于真实 `dist/index.html`;
  ② `app.js` 里 `call("M")` 调用的每个绑定方法都在 `stub.js` 里有桩;
  ③ `index.html` 仍只有一个 `app.js` 标签(真页面生成脚本依赖)。
- `wp.test.mjs` / `write.test.mjs` / `editor.test.mjs` / `bus.test.mjs`: 用 `harness.mjs`
  (极简 DOM + Node vm)加载**真实的** `dist/app.js`, 断言 JS↔Go 绑定契约(方法名、
  参数形态——例如 `WPSet` 必须传数字数组而不是 `[]byte` 能吃的形态——返回值用法)
  与渲染关键文案。本项目两次线上的"点了没反应"都是这一层能拦下来的问题。

## 2. 真实浏览器冒烟(真 DOM / 真 CSS)

Node 夹具不是真 DOM(`classList.toggle(cls, force)`、`querySelector`、布局都必须真浏览器验)。
两种跑法, 结论都写进 `window.__SMOKE`:

```bash
node frontend/test/make-real-page.mjs   # 从 dist/index.html 生成 _real-page.html
```

- `_real-page.html`(**推荐**): 由脚本从**真正发布的那份 `dist/index.html`** 生成,
  只在 `app.js` 前插桩、之后插驱动。测的就是发布页面的 DOM 结构, 不存在"骨架副本漂移"。
- `smoke.html`: 手写骨架版, 好处是可以不看 `index.html` 单独调 UI。

在平台的 agent 浏览器里打开对应 `file://` 地址, 等 ~2 秒后读:
`window.__SMOKE.steps`(全部 `ok`)与 `window.__ERRORS`(必须为空)。

## 3. 文件说明

| 文件 | 作用 |
|---|---|
| `stub.js` | `window.go.app.App` 全量桩(冒烟与真页面注入共用) |
| `driver.js` | 按真实用户顺序点一遍的驱动脚本, 结果写 `window.__SMOKE` |
| `smoke.html` | 手写骨架页面 |
| `make-real-page.mjs` | 从 `dist/index.html` 生成 `_real-page.html`(生成物已 gitignore) |
| `harness.mjs` | Node vm + 极简 DOM 夹具, 供第 1 层使用 |
