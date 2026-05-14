# AutoClip Architecture

AutoClip 分成两层：

- **AutoClip Skill**：让 Agent 用中文理解用户意图、选择剪辑力度、解释依赖档位、执行 CLI、总结报告。
- **`autoclip` CLI**：执行本地依赖管理、媒体探测、音频提取、whisper.cpp 转写、剪辑规划、渲染和报告生成。

## 数据流

```text
用户中文请求
  -> AutoClip Skill
  -> autoclip install/doctor
  -> autoclip transcribe
  -> FFmpeg 提取音频与静音检测
  -> whisper.cpp 本地转写
  -> 可选：Agent/LLM 校字，生成 corrected transcript
  -> autoclip analyze 或 render
  -> 词级口癖/静音/边缘静音候选
  -> cut plan
  -> 候选预览 report
  -> FFmpeg 音频增强/字幕烧录/渲染
  -> preview + report + manifest
```

## 本地依赖

依赖由 `release/dependency-manifest.json` 驱动。模型和二进制下载必须校验 checksum。manifest 支持 `mirror_urls`，安装器会按官方源和备用镜像依次尝试，保留 `.download` 临时文件做断点续传，最后仍以 checksum 作为唯一可信依据。当前清单已固定 whisper.cpp 源码包和模型；FFmpeg/ffprobe 会优先使用 PATH 或 `AUTOCLIP_HOME/bin`，也支持后续在 `binaries` 数组里补入带 checksum 的平台二进制下载项。`best` 档位在发布前需要重新核对官方来源，确保它仍然指向当时可确认的本地最高质量多语言 ASR 模型。

## 渲染策略

首版实现：

- `accurate`：默认，使用 FFmpeg filter graph 拼接保留片段并重编码。
- `audio-only`：只输出清理后的音频。
- `--audio-enhance voice`：在拼接后应用轻量降噪、静音门限和 `loudnorm`。
- `--burn-subtitles`：使用生成的 ASS 字幕烧录到视频。
- `--variants`：复用同一份 transcript，批量生成多个剪辑力度版本。

`fast-copy` 和 timeline export 为后续扩展。

## 批处理

批处理会逐个文件独立处理，单个失败不会阻断其他文件。`--cache` 会按输入文件 hash、语言、模型和时间戳设置缓存 transcript；`--resume` 会跳过已有完整产物。批处理报告包含成功/失败/跳过状态和节省时长排行。

## Agent 校字边界

CLI 不直接内置云端 LLM 调用。它负责生成 `*.transcript.review.json` 和 `*.transcript.review.md`，并在 `transcript apply` 阶段校验 Agent 返回的 corrected transcript。Agent 只允许改字幕文本，不允许改时间戳；CLI 会保留原始 `id/start/end`，生成 corrected JSON/SRT，并可通过 `--transcript` 用于后续分析和渲染。
