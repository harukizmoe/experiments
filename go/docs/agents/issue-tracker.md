# 问题追踪器：GitHub

本项目的问题与规格文档使用 GitHub Issues 管理，统一通过 `gh` CLI 操作。

## 约定

- 创建 Issue：`gh issue create --title "..." --body "..."`
- 读取 Issue：`gh issue view <number> --comments`
- 列出 Issue：`gh issue list --state open`
- 添加评论：`gh issue comment <number> --body "..."`
- 添加或移除标签：`gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- 关闭 Issue：`gh issue close <number> --reason completed`
- GitHub 仓库：`harukizmoe/go-exp`

## 发布到问题追踪器

当技能要求发布问题或任务时，创建 GitHub Issue，不再新增 `.scratch/<feature>/issues/` 任务文件。

规格文档可以继续保存在仓库中的 Markdown 文件内，并在 Issue 正文中链接对应路径。

## Wayfinding

- Map：创建一个带 `wayfinder:map` 标签的 GitHub Issue。
- 子任务：创建独立 GitHub Issue；优先使用 GitHub sub-issues 关联 Map。
- 阻塞关系：优先使用 GitHub 原生 Issue dependencies；不支持时在子任务正文顶部记录 `Blocked by: #<n>`。
- 认领任务：`gh issue edit <n> --add-assignee @me`。
- 解决任务：先追加答案评论，再关闭 Issue，并在 Map 的 Decisions-so-far 中记录上下文链接。

## Pull requests

PR 不作为 triage 请求来源。实现 Issue 时，PR 正文使用 `Closes #<n>` 或 `Fixes #<n>`，使合入后 GitHub 自动关闭对应 Issue。
