# AutoClip

AutoClip 是一个本地优先的 AI Agent 视频粗剪项目。用户用中文描述“去口癖、去长停顿、给我几个版本挑一挑”，Agent 负责理解意图和校字，`autoclip` CLI 负责本地执行转写、剪辑规划、渲染和报告生成。

当前仓库状态：`v0.1 alpha`

- 默认本地运行，不上传音视频。
- 默认转写引擎是 `whisper.cpp`。
- 输出不仅有成片，还会保留 transcript、候选预览、cuts JSON、report、manifest，方便审计和复现。
- 首个可运行目标是 macOS，代码结构保留了后续扩展 Linux / Windows 的空间。

## 它解决什么问题

AutoClip 不是一个“黑盒一键神剪辑”工具，而是一个更适合 Agent 和可审计工作流的粗剪系统：

- 你可以先看 transcript、候选剪辑点和预览，再决定是否渲染。
- 你可以先让本地 ASR 转写，再让 Agent 按上下文修正同音词、专名、术语和断句。
- 你可以一次生成保守版、标准版、激进版，而不是只吐出一个结果。
- 批处理时，单个坏文件不会拖死整批任务。

## 组成

AutoClip 分成两层：

- `skill/`：给 Agent 用的 Skill，负责中文交互、模型档位解释、工作流编排、失败恢复和结果总结。
- `cli/`：Go 写的 `autoclip` 命令行，负责本地依赖、媒体探测、转写、剪辑规划、渲染和报告生成。

更完整的说明见 [docs/architecture.md](docs/architecture.md) 和 [docs/cli.md](docs/cli.md)。

## 快速开始

### 1. 准备环境

建议先保证系统里有：

- Go
- FFmpeg

`autoclip install` 会准备 `whisper-cli` 和所选模型；当前版本会优先复用系统里的 `FFmpeg`，如果缺失，`doctor` 会明确提示。

### 2. 用 Homebrew 安装

如果你希望 Agent 或本机环境更容易复用，推荐直接用 Homebrew tap：

```bash
brew tap shenyangs/autoclip
brew install shenyangs/autoclip/autoclip
autoclip models list
autoclip install --profile fast-high
autoclip doctor
```

说明：

- 这个 tap 会安装 `autoclip` CLI，并通过 Homebrew 安装 `ffmpeg` 依赖。
- 首次使用前仍然需要显式选择并安装本地转写模型。
- 如果你追求当前最强的本地模型，把 `fast-high` 换成 `best` 即可。

### 3. 构建 CLI

仓库内最简单的方式：

```bash
bash skill/scripts/ensure-autoclip.sh
```

或者手动构建：

```bash
cd cli
go build -o autoclip ./cmd/autoclip
cd ..
```

下文都假设你从仓库根目录运行 `./cli/autoclip`。

### 4. 选择本地模型档位

先看可选档位：

```bash
./cli/autoclip models list
```

当前内置四档：

| 档位 | 默认模型 | 大小 | 适合场景 |
|---|---|---:|---|
| `lite` | `base` | 142 MiB | 试用、低配机器、短视频 |
| `standard` | `small` | 466 MiB | 日常粗剪 |
| `fast-high` | `large-v3-turbo-q5_0` | 547 MiB | 大多数正式使用，推荐 |
| `best` | `large-v3` | 2.9 GiB | 质量优先、重要素材 |

安装示例：

```bash
./cli/autoclip install --profile fast-high
./cli/autoclip doctor
```

如果你要直接跑当前最强的本地模型：

```bash
./cli/autoclip install --profile best
```

注意：`best` 首次下载约 2.9 GiB，不会静默下载。复杂网络下可以重跑同一条命令，CLI 会续传并自动切换备用镜像，最后以 checksum 校验为准。

### 5. 跑一次最小闭环

```bash
./cli/autoclip render input.mp4 \
  --preset balanced \
  --language zh \
  --timestamps word \
  --audio-enhance voice \
  --trim-edges
```

这会生成成片、字幕、剪辑点、预览、报告和 manifest。

## 常见工作流

### 直接粗剪

```bash
./cli/autoclip render input.mp4 \
  --preset balanced \
  --language zh \
  --timestamps word
```

适合先快速看一个可用版本。

### 先转写，再让 Agent 校字

如果视频里有专名、品牌名、术语、同音词，推荐走这条流程：

```bash
./cli/autoclip transcribe input.mp4 \
  --language zh \
  --timestamps word \
  --review-pack \
  --context "素材主题和场景说明" \
  --glossary "专名1,专名2"
```

它会生成：

- `*.transcript.json`
- `*.transcript.srt`
- `*.transcript.review.json`
- `*.transcript.review.md`

然后让 Agent 只改字幕文本，不改 `id/start/end`，再应用校字结果：

```bash
./cli/autoclip transcript apply \
  input.autoclip.transcript.json \
  corrected.json
```

再把 corrected transcript 用回后续分析或渲染：

```bash
./cli/autoclip render input.mp4 \
  --language zh \
  --transcript input.autoclip.transcript.corrected.json \
  --preset balanced
```

`transcript apply` 会保留原时间轴，同时清理 0 秒字幕段，并合并紧邻的重复字幕，避免 ASR 尾段重复直接进入成片。

### 先看候选，不急着渲染

```bash
./cli/autoclip analyze input.mp4 \
  --preset balanced \
  --language zh \
  --timestamps word
```

会生成 `*.preview.md` 和 `*.preview.html`，用于复核“已剪 / 建议人工看 / 保护词保留”的候选项。

### 一次生成多个版本

```bash
./cli/autoclip render input.mp4 \
  --variants conservative,balanced,aggressive \
  --language zh \
  --cache
```

适合快速挑一个节奏。

### 批量处理目录

```bash
./cli/autoclip batch ./videos \
  --preset conservative \
  --language zh \
  --cache \
  --resume \
  --audio-enhance voice
```

批处理会继续处理失败文件，并输出 `batch.report.md`。

## 输出内容

单视频流程通常会生成这些文件：

- `*.autoclip.mp4` 或 `*.autoclip.m4a`
- `*.autoclip.cuts.json`
- `*.autoclip.transcript.json`
- `*.autoclip.transcript.srt`
- `*.autoclip.transcript.ass`
- `*.autoclip.transcript.review.json`
- `*.autoclip.transcript.corrected.json`
- `*.autoclip.preview.md`
- `*.autoclip.preview.html`
- `*.autoclip.report.md`
- `*.autoclip.manifest.json`

这套产物的目的不是“多生成点文件”，而是让每一步都能复盘、审阅和复现。

## 下载与可靠性

模型和依赖清单由 [release/dependency-manifest.json](release/dependency-manifest.json) 驱动。

- 安装器支持主源和多个镜像源。
- 下载中断后会复用 `.download` 临时文件尝试续传。
- 下载完成后必须通过 checksum 校验。
- `models refresh` 会联网刷新官方模型表，非交互环境下需要显式传 `--yes`。

如果下载慢或失败，直接重跑原命令通常就是正确恢复方式：

```bash
./cli/autoclip install --profile best
```

## 隐私边界

当前版本默认只启用 `--provider local`。

- 音视频默认留在本机。
- `openai` provider 目前只预留接口，没有在本版本上传媒体。
- Agent 校字能力依赖外层 Agent / LLM；CLI 本身只负责生成 review pack 和应用 corrected transcript。

## 当前范围与限制

现在这个仓库已经适合以 alpha 形态公开试用，但它还不是“稳定版一键生产系统”。当前边界是：

- 首版优先 macOS。
- 默认渲染模式是 `accurate`，会重编码。
- `fast-copy`、timeline export、云端转写 provider 还没有正式交付。
- 字幕校字依赖外层 Agent 理解上下文，不是 CLI 自带大模型推理。
- 仓库不附带公开视频样例；请使用你自己的本地媒体测试。

## 仓库结构

```text
.
├── cli/        Go CLI
├── docs/       CLI 与架构说明
├── release/    依赖与模型 manifest
├── skill/      Agent Skill、提示和辅助脚本
└── README.md
```

## 开发

运行测试：

```bash
cd cli
go test ./...
```

查看模型清单：

```bash
./cli/autoclip models list
```

查看环境状态：

```bash
./cli/autoclip doctor
```

## 文档

- [docs/cli.md](docs/cli.md)
- [docs/architecture.md](docs/architecture.md)
- [skill/SKILL.md](skill/SKILL.md)

## 适合什么口径开源

建议把 AutoClip 当作：

`本地优先的 AI Agent 视频粗剪工具`

而不是：

`全自动、零复核、生产级神剪辑系统`

这个定位更真实，也更利于后续迭代。

## License

MIT. See [LICENSE](LICENSE).
