# 实验主目录

各类实验的统一主目录：多语言、多主题，实验彼此独立。原 `Go/goexp` 仓库已并入本仓库的 [`go/`](go) 目录，本仓库接管其 GitHub 远端。

## 目录布局

```text
experiments/
├── README.md
├── AGENTS.md
├── go/                       # Go 实验：单一 module，沿用 goexp 约定
│   ├── go.mod
│   ├── AGENTS.md
│   ├── CONTEXT.md
│   ├── docs/
│   └── experiments/
│       └── <experiment-name>/
└── <experiment-name>/        # 其他语言实验：自带工具链清单
    ├── README.md
    ├── Cargo.toml            # 或 pyproject.toml / package.json
    └── ...
```

- 目录名 kebab-case，短、稳定、语义唯一，例如 `esp32-ble-probe`、`lua-mpv-ipc`。
- `go/` 之外没有根级工作区清单：每个实验的依赖锁在自己的清单文件里，互不污染。
- 混合技术栈的实验（如 Rust 服务 + Python 脚本）放在同一个实验目录，各自保留清单。
- 不预创建空目录；实验落地时才建目录。

## Go 实验

Go 实验统一放在 `go/`，共享一个 `go.mod`（module 名 `harukizmoe/experiments/go`），运行方式与约定见 [`go/README.md`](go/README.md) 与 [`go/AGENTS.md`](go/AGENTS.md)：

```bash
cd go
go run ./experiments/<name>
go test ./...
```

不要把 Go 实验拆到仓库根目录，也不要在根目录新增第二个 `go.mod`。

## 规范

完整约定见 `AGENTS.md`，要点：

- 实验之间不共享代码、不互相导入。
- 每个实验的 `README.md` 必须写明：目的、待验证的问题、关键概念、运行命令、预期现象、结论；结论以实测数据为准，未验证标记「待验证」。
- 代码注释用中文，解释原因与约束；标识符与命令保留原文。
- 构建产物、依赖目录、`.env`、本地数据不入库。

## 快速开始

```bash
# Go 实验
cd go && go test ./...

# 新建其他语言实验（以 Rust 为例）
cargo new --bin <experiment-name>
$EDITOR <experiment-name>/README.md
```

## 历史

原 `harukizmoe/go-exp` 的全部提交历史经 `go/` 前缀重写后并入本仓库；迁移前的分支顶端保存在 `legacy/pre-migration`。
