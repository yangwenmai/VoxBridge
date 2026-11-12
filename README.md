# VoxBridge

给链接就能听懂。粘贴一个直播链接，或者一个视频、音频链接，VoxBridge 解析出音频，然后二选一：

- **实时同传**：边播边出目标语言的译音和字幕。直播只能走这条。
- **预先配音**：先把整集翻译、合成好再播放，中文按句对齐。只适用于点播。

接入豆包（火山引擎）、Gemini、OpenAI，接口留好，方便社区接入其他厂商。你自带 API Key，项目不代付，也不跑托管服务。

> 项目处于早期开发阶段。目前能做的：检查环境和 Key（`doctor`），把链接解析成直播或点播来源（`parse`）。同传和配音还没做。

## 安装

需要 Go 1.25+，以及系统里的 ffmpeg 和 yt-dlp（VoxBridge 不打包它们）：

```bash
brew install ffmpeg yt-dlp        # macOS
go install github.com/yangwenmai/voxbridge/cmd/voxbridge@latest
```

## 配置 API Key

至少配一家。环境变量优先：

```bash
export DOUBAO_API_KEY=...   # 火山引擎控制台「豆包语音 → API Key 管理」，新版控制台的 API Key
export GEMINI_API_KEY=...   # https://aistudio.google.com/apikey
export OPENAI_API_KEY=...   # https://platform.openai.com/api-keys
```

也可以写进 `~/.config/voxbridge/config.json`：

```json
{"providers": {"doubao": {"apiKey": "..."}, "gemini": {"apiKey": "..."}, "openai": {"apiKey": "..."}}}
```

Key 只用于发给对应厂商的请求，不写进日志、输出文件或仓库。

## 使用

```bash
voxbridge doctor
voxbridge parse "https://www.youtube.com/watch?v=arj7oStGLkU"
voxbridge parse "https://www.youtube.com/@aljazeeraenglish/live" --sample live.wav --seconds 10
```

`doctor` 对每个配了 Key 的厂商发一次最小请求，报告哪些能力能用。豆包会产生约 10 个 token 和 3 个字符的用量（在免费额度内），Gemini 和 OpenAI 不发音频、不计费。

`parse` 把链接分成直播（只能同传）和点播（同传或配音），带上平台字幕。`--sample` 把前几秒解码成 16 kHz 单声道 wav，方便试听。

各厂商的模型、语言和价格见 [docs/providers.md](docs/providers.md)。

## 代码结构

| 目录 | 内容 |
| --- | --- |
| `core` | 数据形状：Source、Interpreter / InterpretSession、Recognizer / Translator / Synthesizer、DubManifest、能力表、错误分类 |
| `provider/doubao`、`provider/gemini`、`provider/openai` | 每家一个包，导出能力表和 doctor 检查 |
| `provider/registry` | 内置厂商列表。接入新厂商 = 新包 + 这里一行 |
| `source` | 用 yt-dlp、ffprobe 把链接解析成 Source |
| `media` | 调用系统 ffmpeg / ffprobe / yt-dlp |
| `config` | 读 Key |
| `cli`、`cmd/voxbridge` | 命令行 |

## 版权与使用责任

VoxBridge 只是在你自己的电脑上处理你提供的链接。**你要对所处理内容的版权和使用方式负责**，请确认你有权收听、转写、翻译和保存这些内容，并遵守来源平台的服务条款。

VoxBridge 不绕过 DRM，不做登录抓取，不批量处理整个频道。

## 许可证

[Apache-2.0](LICENSE)
