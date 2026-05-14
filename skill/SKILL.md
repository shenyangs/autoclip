---
name: autoclip
description: Use when the user asks an AI agent to clean, rough-cut, trim, auto-edit, remove filler words, remove silences, or batch process local video/audio files. The skill installs and runs the autoclip CLI, which uses FFmpeg and local whisper.cpp transcription to generate edited media plus auditable cut reports.
---

# AutoClip Skill

用中文和用户沟通。AutoClip 是本地优先的视频/音频粗剪工具，优先运行 `autoclip` CLI，不要手写复杂 FFmpeg 管线替代 CLI。

## 触发条件

当用户要求清理视频、去口癖、去停顿、粗剪、自动剪辑、批量处理视频/音频时使用本 Skill。

## 首次运行

1. 找到用户要处理的媒体路径。
2. 运行 `skill/scripts/ensure-autoclip.sh` 或 Windows 下运行 `skill/scripts/ensure-autoclip.ps1`。
3. 运行 `autoclip doctor`。
4. 如果未安装本地模型，先向用户解释四个档位并让用户选择：
   - `lite`：小体积、下载快、能力基础，适合试用。
   - `standard`：体积中等、能力中等，适合日常粗剪。
   - `fast-high`：质量高且更快，推荐正式使用。
   - `best`：体积最大、质量优先，是当前发布清单中的本地最佳质量选择。
5. 在用户选择前必须提示：模型首次下载可能耗时较长，`best` 约 2.9 GiB，复杂网络下可能需要多次重试；AutoClip 会按官方源和备用镜像依次尝试，支持断点续传，并在完成后校验 checksum。
6. 用户选择后运行 `autoclip install --profile <档位>`。不要静默下载大模型，也不要绕过 checksum。

## 预设选择

- 用户说“自然一点、别剪太狠、安全、保守”：使用 `--preset conservative`。
- 用户说“清一下、去口癖、去长停顿”：使用 `--preset balanced`。
- 用户说“剪紧一点、压缩、激进、短一点”：使用 `--preset aggressive`，并提醒可能影响自然节奏。

## 中文口癖

默认可以让 CLI 使用内置中文口癖和保护词。用户明确指定时才覆盖：

```bash
autoclip render input.mp4 \
  --language zh \
  --filler "嗯,呃,啊,那个,就是" \
  --protect "然后" \
  --preset balanced
```

## 隐私规则

首版默认本地 whisper.cpp 转写。不要暗示会上传媒体。若用户要求云端转写，说明接口已预留但当前版本未启用。

## 工作流

- 只分析：`autoclip analyze input.mp4 --preset balanced --language zh --timestamps word`
- 渲染单个视频：`autoclip render input.mp4 --preset conservative --language zh --audio-enhance voice`
- 批处理：`autoclip batch ./videos --preset conservative --language zh --cache --resume`
- 多档输出：`autoclip render input.mp4 --variants conservative,balanced,aggressive --language zh --cache`
- 总结已有结果：`autoclip explain input.autoclip.manifest.json`

## 增强能力

- 用户要求“口癖剪准一点、别误剪”：加 `--timestamps word`，CLI 会优先剪独立低风险口癖，嵌在语句里的候选进入 preview。
- 用户要求“给我看剪了什么”：读取 `*.preview.md` 或 `*.preview.html`，说明已剪、建议人工看、保护词保留。
- 用户要求“声音处理一下”：加 `--audio-enhance voice`；只要音量标准化用 `--loudnorm`，只要降噪/门限用 `--denoise`。
- 用户要求“开头结尾空白也裁掉”：加 `--trim-edges`。
- 用户要求“字幕好看/带字幕视频”：默认已有 SRT/ASS；需要内嵌字幕时加 `--burn-subtitles --subtitle-style large`。
- 用户要求“给我几个版本挑”：用 `--variants conservative,balanced,aggressive`。

## Agent 校字

当用户希望字幕更准、要求修同音词/专名/术语，或素材适合发布时，使用二段式校字：

1. 运行 `autoclip transcribe input.mp4 --language zh --review-pack --context "<素材说明>"`。
2. 读取 `*.transcript.review.json` 和 `*.transcript.review.md`。
3. 用 Agent 自己的语言能力校准字幕：只改 segment 的 `text`，保留 `id/start/end`；不要润色成书面语，不要增删真实意思。
4. 将校字结果写成 corrected JSON，结构保持 `schema_version/language/segments/metadata`。
5. 运行 `autoclip transcript apply original.transcript.json corrected.json`。
6. 后续分析/渲染传入 `--transcript *.transcript.corrected.json`，让剪辑和字幕使用校准后的文本。

`transcript apply` 会保留时间轴，同时清理 0 秒字幕段并合并相邻重复字幕；如果原始 ASR 在尾段重复，不要手工改时间戳，交给 CLI 清理。

校字重点：同音词、近音词、专名、产品名、英文缩写、数字、标点和断句。遇到 WPS、金山、办公 AI、AI 办公助手等语境时，`灵溪/灵西/零溪/零犀` 应优先校为产品名 `灵犀`，完整写法可为 `WPS 灵犀`。没有上下文把握时保留原文，不要猜。

## 完成后总结

读取 `*.autoclip.report.md` 或运行 `autoclip explain`，用中文告诉用户：

- 输出视频或音频路径
- 剪辑点数量
- 移除时长
- 报告、字幕、cuts JSON、manifest 路径
- 候选预览 `*.preview.md/html` 路径
- 如果做了 Agent 校字，也说明 corrected transcript/SRT 路径和变更片段数量
- 是否有保留的候选项或保护词
- 批处理时哪些文件失败以及原因

## 失败恢复

- 缺模型：让用户选择档位并运行 `autoclip install --profile <档位>`。
- 模型下载慢或失败：提醒用户保持终端运行；如果中断，重新运行同一条安装命令即可续传。CLI 会尝试 manifest 中的官方源、hf-mirror、MacPorts Aliyun/MIT 等备用源，并最终校验 checksum。
- 缺 FFmpeg：运行 `autoclip doctor`，提示把 FFmpeg 放入 PATH 或 `AUTOCLIP_HOME/bin`。
- 缺 whisper-cli：运行 `autoclip install --profile <档位>`，它会优先使用缓存/系统工具，缺失时尝试本地构建。
- 媒体不支持或损坏：报告该文件失败，批处理继续处理其他文件。
