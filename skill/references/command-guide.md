# AutoClip Command Guide

## 安装

```bash
autoclip models list
autoclip install --profile fast-high
autoclip doctor
```

安装前先提醒用户模型体积和耗时。`best` 约 2.9 GiB；复杂网络下可以重跑同一条命令，CLI 会尝试备用镜像、断点续传并校验 checksum。

## 单文件

```bash
autoclip transcribe input.mp4 --language zh --review-pack --timestamps word --context "素材说明"
autoclip transcript apply input.autoclip.transcript.json corrected.json
autoclip analyze input.mp4 --preset balanced --language zh
autoclip render input.mp4 --preset conservative --language zh --transcript input.autoclip.transcript.corrected.json --audio-enhance voice --burn-subtitles
autoclip render input.mp4 --variants conservative,balanced,aggressive --language zh --cache
```

## 批处理

```bash
autoclip batch ./videos --preset conservative --language zh --cache --resume
```

## 解释结果

```bash
autoclip explain input.autoclip.manifest.json
```
