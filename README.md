# AutoClip

AutoClip 是一个 Agent 编排、本地优先的视频粗剪系统。用户用中文描述“清一下口癖和长停顿”，Agent 调用 `autoclip` CLI 完成转写、剪辑规划、渲染和报告生成。

## 开发

```bash
cd cli
go test ./...
go run ./cmd/autoclip models list
```

## 使用

```bash
autoclip install --profile fast-high
autoclip doctor
autoclip render input.mp4 --preset balanced --language zh --timestamps word
```

首次安装前必须提示用户选择模型档位和下载体积。`best` 使用本地最高质量模型，约 2.9 GiB；网络复杂时可重跑同一条安装命令，CLI 会续传并自动尝试备用镜像，完成后校验 checksum。

需要字幕更准时，先运行 `autoclip transcribe input.mp4 --review-pack`，让 Agent 校准同音词、专名和断句，再用 `autoclip transcript apply` 生成 corrected transcript，后续 `render/analyze` 传 `--transcript` 使用校准后的字幕。
`transcript apply` 会保留原时间轴，同时清理 0 秒字幕段并合并相邻重复字幕。

常用增强：

```bash
autoclip render input.mp4 --audio-enhance voice --trim-edges --burn-subtitles
autoclip render input.mp4 --variants conservative,balanced,aggressive --cache
autoclip batch ./videos --cache --resume
```

单文件会生成剪辑预览 `*.preview.md/html`、SRT/ASS 字幕、报告和 manifest；批处理报告会列出失败项和节省时长排行。

首版默认本地 whisper.cpp 转写；云端转写 provider 只预留接口。
