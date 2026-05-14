# AutoClip Privacy

首版默认只使用本地 whisper.cpp 转写。媒体文件、提取音频和模型推理都在用户机器上完成。

Agent 不应静默上传音频。云端转写 provider 只预留接口，当前版本未启用。

临时音频默认在成功转写后删除；用户传 `--keep-audio` 时才保留。
