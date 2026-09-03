# 问题追踪器：本地 Markdown

本项目的问题与规格文档存放在 `.scratch/` 下。

## 约定

- 每个功能使用一个目录：`.scratch/<feature-slug>/`
- 规格文档：`.scratch/<feature-slug>/spec.md`
- 实现任务：`.scratch/<feature-slug>/issues/<NN>-<slug>.md`
- 任务编号从 `01` 开始，不创建合并后的单一任务文件
- 每个任务文件顶部附近使用 `Status:` 记录状态
- 评论与讨论追加在文件底部的 `## Comments` 标题下

## 发布到问题追踪器

创建 `.scratch/<feature-slug>/` 目录，并在其中写入对应文档。

## 获取任务

读取用户提供路径或任务编号对应的文件。

## Wayfinding

- Map：`.scratch/<effort>/map.md`
- 子任务：`.scratch/<effort>/issues/NN-<slug>.md`
- 子任务使用 `Type:` 记录类型：`research`、`prototype`、`grilling` 或 `task`
- 子任务使用 `Status:` 记录状态：`claimed` 或 `resolved`
- `Blocked by:` 记录阻塞它的任务编号
- 认领任务前先将其状态设为 `claimed`
- 解决任务时追加 `## Answer`，设置状态为 `resolved`，并将上下文指针追加到 map 的 Decisions-so-far
