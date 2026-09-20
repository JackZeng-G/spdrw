# 前端测试

两层验证, 各有分工。**新增绑定方法或页面元素后, 先跑第 1 层**——它会直接告诉你漏了什么。

## 1. Node 契约与漂移守卫(秒级, CI 可跑)

```bash
node --test "frontend/test/*.test.mjs"
```

- `page.test.mjs`: **漂移守卫**。机械校验
  ① `app.js` 里 `$("x")` 用到的每个 id 都存在于真实 `dist/index.html`(`read-mode` 这类
    动态生成的 id 走白名单, 并额外校验 app.js 确实会生成它);
  ② `app.js` 里 `call("M")` 调用的每个绑定方法都在 `stub.js` 里有桩;
  ③ `driver.js` 冒烟驱动用到的元素都在发布页里, 且它反向断言"已删除"的元素确实不在
    (注意 `!getElementById` 是反向断言, `!!getElementById` 是正向断言);
  ④ `index.html` 仍只有一个 `app.js` 标签(真页面生成脚本依赖);
  ⑤ 图例与 hex 改动格的配色必须来自同一组 `--chg-*` 变量(每个色值只许出现一次);
  ⑥ `SPD 内容` 的列头/行偏移必须是 sticky。
- `wp.test.mjs` / `write.test.mjs` / `editor.test.mjs` / `bus.test.mjs` / `xmp3.test.mjs`: 用 `harness.mjs`
  (极简 DOM + Node vm)加载**真实的** `dist/app.js`, 断言 JS↔Go 绑定契约(方法名、
  参数形态——例如 `WPSet` 必须传数字数组而不是 `[]byte` 能吃的形态——返回值用法)
  与渲染关键文案。本项目两次线上的"点了没反应"都是这一层能拦下来的问题。

## 2. 真实浏览器冒烟(真 DOM / 真 CSS)

Node 夹具不是真 DOM(`classList.toggle(cls, force)`、`querySelector`、布局都必须真浏览器验)。
冒烟**只有一个入口**: 从发布页生成 `_real-page.html`, 结论写进 `window.__SMOKE`。

```bash
node frontend/test/make-real-page.mjs   # 从 dist/index.html 生成 _real-page.html
```

`_real-page.html` 由脚本从**真正发布的那份 `dist/index.html`** 生成, 只在 `app.js` 前插桩、
之后插驱动, 所以测的就是发布页面的 DOM 结构与样式, 不存在"骨架副本漂移"。
在平台的 agent 浏览器里打开 `file://…/_real-page.html?v=<时间戳>`, 等 ~2 秒后读:
`window.__SMOKE.steps`(全部 `ok`)与 `window.__ERRORS`(必须为空)。

> **必须带 `?v=<时间戳>`**: 否则浏览器/标签页会命中缓存, 读到的是上一次的页面。
>
> 历史: 早期有一份手写的骨架页 `smoke.html`, 与 `dist/index.html` 双份维护。它后来漂移到
> 驱动直接抛错(`button.q[data-hint="hint-write"]` 在骨架里不存在 → `window.__SMOKE`
> 永远是 undefined, 冒烟**静默失效**), 同时 Node 层全绿给人"冒烟没事"的错觉。
> 现已删除, 只保留从发布页生成的这一条路。

## 3. 文件说明

| 文件 | 作用 |
|---|---|
| `stub.js` | `window.go.app.App` 全量桩(冒烟与真页面注入共用) |
| `driver.js` | 按真实用户顺序点一遍的驱动脚本, 结果写 `window.__SMOKE` |
| `make-real-page.mjs` | 从 `dist/index.html` 生成 `_real-page.html`(生成物已 gitignore) |
| `harness.mjs` | Node vm + 极简 DOM 夹具, 供第 1 层使用 |
