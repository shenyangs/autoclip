# AutoClip Troubleshooting

- `首次安装必须显式选择本地模型档位`：运行 `autoclip install --profile lite|standard|fast-high|best`。
- 下载耗时或失败：先告诉用户模型较大，尤其 `best` 约 2.9 GiB。让用户重跑同一条 `autoclip install --profile <档位>`，CLI 会复用 `.download` 临时文件尝试断点续传，并自动切换官方源/备用镜像。
- checksum 不匹配：不要手动信任该文件。让 CLI 删除临时文件后重试其他来源；仍失败时运行 `autoclip models list` 检查当前 manifest 的 checksum 和来源。
- `未找到 whisper-cli`：运行安装命令；如果本地构建失败，检查是否有 `make` 和 Xcode Command Line Tools。
- `未找到 ffmpeg`：将 FFmpeg 放入 PATH 或 `AUTOCLIP_HOME/bin`。
- `云端转写接口已预留`：当前版本只支持本地转写。
- 批处理部分失败：查看 `batch.report.md`，失败文件不会阻断其他文件。
