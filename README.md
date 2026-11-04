# 实验主目录

各类实验的统一主目录：多语言、多主题，实验彼此独立。

**Go 专项实验不在这里**，仍在 [`../Go/goexp`](../Go/goexp)（单一 module + `experiments/` 子目录）。本目录只放其他语言或以其他技术栈为中心的实验。

## 目录布局

```text
experiments/
├── README.md
├── AGENTS.md
└── <experiment-name>/        # 一个实验一个目录，自带工具链清单
    ├── README.md             # 目的、待验证问题、运行命令、实测结论
    ├── Cargo.toml            # 或 go.mod / pyproject.toml / package.json
    └── ...
```

- 目录名 kebab-case，短、稳定、语义唯一，例如 `esp32-ble-probe`、`lua-mpv-ipc`。
- 没有根级工作区清单：每个实验的依赖锁在自己的清单文件里，互不污染。
- 混合技术栈的实验（如 Rust 服务 + Python 脚本）放在同一个实验目录，各自保留清单。
- 不预创建空目录；实验落地时才建目录。

## 与 `../Go/goexp` 的边界

| | `../Go/goexp` | 本目录 |
| --- | --- | --- |
| 语言 | 仅 Go | 其他语言 / 混合栈 |
| 构建 | 单一 `go.mod` | 每个实验独立清单 |
| 运行 | `go run ./experiments/<name>` | 各自 README 里的命令 |

需要 Go 与其他语言协作时，Go 侧留在 `goexp`，另一侧在本目录，跨仓交互写成网络/进程边界并在两侧 README 互相引用。

## 规范

完整约定见 `AGENTS.md`，要点：

- 实验之间不共享代码、不互相导入。
- 每个实验的 `README.md` 必须写明：目的、待验证的问题、关键概念、运行命令、预期现象、结论；结论以实测数据为准，未验证标记「待验证」。
- 代码注释用中文，解释原因与约束；标识符与命令保留原文。
- 构建产物、依赖目录、`.env`、本地数据不入库。

## 快速开始

```bash
# 新建一个实验（以 Rust 为例）
cargo new --bin <experiment-name>
$EDITOR <experiment-name>/README.md

# 运行与验证交给各实验自己的工具链
cd <experiment-name> && cargo test
```
