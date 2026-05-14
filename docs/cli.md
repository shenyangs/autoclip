# AutoClip CLI

AutoClip 是本地优先的视频粗剪工具。CLI 负责确定性执行，Agent Skill 负责中文交互和流程编排。

## 首次安装

首次使用必须选择本地模型档位，AutoClip 不会静默下载大模型。

```bash
autoclip models list
autoclip install --profile fast-high
autoclip doctor
```

可选档位：

| 档位 | 默认模型 | 说明 |
|---|---|---|
| `lite` | `base` | 小体积、下载快、适合试用和低配机器 |
| `standard` | `small` | 中等体积，适合日常粗剪 |
| `fast-high` | `large-v3-turbo-q5_0` | 高质量且更快，推荐正式使用 |
| `best` | `large-v3` | 质量优先，作为当前发布清单里的本地最佳质量选择 |

## 下载可靠性

模型下载可能耗时较长，尤其 `best` 档位约 2.9 GiB。安装前应明确告诉用户下载体积和网络风险，不要静默拉取大模型。

`autoclip install` 会按 manifest 中的主源和备用镜像依次尝试下载，并支持 `.download` 临时文件断点续传。每次下载完成后都必须通过 checksum 校验；checksum 不匹配时会丢弃临时文件并尝试下一个来源。

当前模型来源：

- 主源：`https://huggingface.co/ggerganov/whisper.cpp/resolve/main/...`
- 备用源：`https://hf-mirror.com/ggerganov/whisper.cpp/resolve/main/...`
- 备用源：`https://mirrors.aliyun.com/macports/distfiles/whisper/...`
- 备用源：`https://mirrors.mit.edu/macports/distfiles/whisper/...`

使用 `autoclip models list` 查看每个档位的模型、大小、checksum、主源和备用源。

## 常用命令

```bash
autoclip probe input.mp4
autoclip transcribe input.mp4 --language zh --review-pack
autoclip transcript review input.autoclip.transcript.json --context "素材主题"
autoclip transcript apply input.autoclip.transcript.json input.corrected.json
autoclip analyze input.mp4 --preset balanced --language zh --transcript input.autoclip.transcript.corrected.json
autoclip render input.mp4 --preset conservative --language zh --transcript input.autoclip.transcript.corrected.json --audio-enhance voice --burn-subtitles
autoclip render input.mp4 --variants conservative,balanced,aggressive --language zh --cache
autoclip batch ./videos --preset conservative --cache --resume
autoclip explain input.autoclip.manifest.json
```

## Agent 校字

本地 whisper.cpp 先生成初版 transcript。随后可以让 Agent/LLM 通读字幕，校准同音词、专名、英文缩写、数字、断句和标点。

```bash
autoclip transcribe input.mp4 --language zh --review-pack --timestamps word --context "这是一段 AutoClip 产品演示"
```

这会生成：

- `*.transcript.json`：初版结构化字幕。
- `*.transcript.srt`：初版 SRT。
- `*.transcript.review.json`：给 Agent 读取和修改的校字包。
- `*.transcript.review.md`：给人看的校字说明和可疑片段列表。

Agent 校字时只改 segment 的 `text`，必须保留 `id/start/end`。遇到 WPS/金山/办公 AI 语境时，`灵溪/灵西/零溪/零犀` 应优先校为产品名 `灵犀` 或 `WPS 灵犀`。校字完成后运行：

```bash
autoclip transcript apply input.autoclip.transcript.json corrected.json
```

CLI 会保留原始时间戳，生成 `*.transcript.corrected.json` 和 `*.transcript.corrected.srt`。后续分析或渲染可以传：
应用校字时会清理 0 秒字幕段，并合并时间上紧贴的重复字幕，避免 ASR 尾段重复直接进入成片。

```bash
autoclip render input.mp4 --language zh --transcript input.autoclip.transcript.corrected.json
```

## 剪辑质量

词级口癖剪辑需要显式开启 `--timestamps word`。CLI 会优先剪独立、低风险的口癖词；嵌在正常语句里的“这个、就是、那个”会进入候选预览，不会直接误剪。

```bash
autoclip analyze input.mp4 --language zh --timestamps word --preset balanced
```

每次分析/渲染会生成：

- `*.preview.md`
- `*.preview.html`

预览里会列出“已剪 / 建议人工看 / 保护词保留”的候选项。

## 音频与字幕

```bash
autoclip render input.mp4 --audio-enhance voice --trim-edges --burn-subtitles --subtitle-style large
```

- `--audio-enhance voice`：启用轻量降噪、静音门限和 `loudnorm`。
- `--loudnorm`：只启用音量标准化。
- `--denoise`：只启用轻量降噪和静音门限。
- `--trim-edges`：允许裁掉开头/结尾长静音。
- `--burn-subtitles`：把生成的 ASS 字幕烧录进视频。
- `--subtitle-style large`：使用更大的字幕样式。

默认会同时输出 SRT 和 ASS。

## 批处理与多档输出

```bash
autoclip batch ./videos --preset conservative --cache --resume --audio-enhance voice
autoclip render input.mp4 --variants conservative,balanced,aggressive --cache
```

- `--cache`：复用同一输入、模型、语言和时间戳设置下的 transcript。
- `--resume`：已有完整产物时跳过。
- `--variants`：同一视频一次生成保守版、标准版、激进版。

批处理报告会继续处理失败文件后的其他文件，并生成节省时长排行。

## 产物

单视频处理会生成：

- `*.autoclip.mp4` 或 `*.autoclip.m4a`
- `*.autoclip.cuts.json`
- `*.autoclip.transcript.srt`
- `*.autoclip.transcript.ass`
- `*.autoclip.transcript.json`
- `*.autoclip.transcript.review.json`
- `*.autoclip.preview.md`
- `*.autoclip.preview.html`
- `*.autoclip.transcript.corrected.srt`
- `*.autoclip.report.md`
- `*.autoclip.manifest.json`

## 隐私

首版默认只启用 `--provider local`。`openai` provider 的接口已预留，但不会在本版本上传音频。
