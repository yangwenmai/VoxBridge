# VoxBridge

给链接就能听懂。粘贴一个直播链接，或者一个视频、音频链接，VoxBridge 解析出音频，然后二选一：

- **实时同传**：边播边出目标语言的译音和字幕。直播只能走这条。
- **预先配音**：先把整集翻译、合成好再播放，中文按句对齐。只适用于点播。

接入豆包（火山引擎）、Gemini、OpenAI，接口留好，方便社区接入其他厂商。你自带 API Key，项目不代付，也不跑托管服务。

> 项目处于早期开发阶段。目前能做的：检查环境和 Key（`doctor`），把链接解析成直播或点播来源（`parse`），点播预先配音（`dub`，只接了豆包）。实时同传还没做。

## 安装

需要 Go 1.25+，以及系统里的 ffmpeg 和 yt-dlp（VoxBridge 不打包它们）：

```bash
brew install ffmpeg yt-dlp        # macOS
go install github.com/yangwenmai/voxbridge/cmd/voxbridge@latest
```

从源码装（在仓库目录里）：

```bash
go install ./cmd/voxbridge        # 装到 $(go env GOPATH)/bin，确保它在 PATH 里
voxbridge help
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

### 预先配音（点播）

```bash
voxbridge dub "https://www.youtube.com/watch?v=H14bBuluwB8" --out grit
afplay grit/dub.m4a
```

- 有平台字幕就直接用（人工字幕优先，其次平台对原声的自动字幕）。
- 没有平台字幕时，**默认不调用远程语音识别**，因为它要上传整集音频并按小时计费：
  - 在终端里运行：显示时长、识别厂商和预估费用，问「要上传音频做语音识别吗？[y/N]」。直接回车或输入 n 就退出，不发任何请求。
  - 非交互运行（stdin 被重定向、管道、脚本、CI）：直接退出，提示加 `--asr` 重跑。
  - 加 `--asr` 表示已确认，不再询问；有平台字幕时加 `--asr` 则改用识别、不用字幕。
  - 工作目录里已经有 `manifest.json`（以前识别过）时不再问，也不会再识别。
- 每句翻译成中文（每批 16 句），再用语音合成 2.0 每句合成一个 mp3，两路并发。每批翻译、每句合成完都写一次 `manifest.json`。
- 中途 Ctrl-C 或出错，再跑同样的命令会接着做，已完成的句子不再发请求。结束时打印本次各服务的请求次数和按公开价估算的费用。
- 对齐：每句中文从 max(原句开始, 上一句中文结束) 开始念。中文没念完时，原声停在下一句开头等，插入静音，后面整体顺延。原声全程压到 25%（`--original-volume` 调，0 为静音）。
- 至少八成句子带说话人标签时，按说话总时长给每位说话人分一个不同的公共音色。
- 输出在工作目录里：`dub.m4a`（混好的音频）、`dub.srt`（中文在上、原文在下，时间轴对应 `dub.m4a`）、`lines/`（每句的 mp3）、`original.flac`（原声缓存）。不加 `--out` 时目录是 `./voxbridge-dubs/<链接哈希>`。
- 费用记在你自己的火山引擎账上。英文按公开价约每小时 5 元（机器翻译 + 语音合成 2.0），走识别再加约 4.5 元/小时。语音识别用的是「大模型录音文件识别极速版」，要先在控制台开通。

各厂商的模型、语言和价格见 [docs/providers.md](docs/providers.md)。

## 代码结构

| 目录 | 内容 |
| --- | --- |
| `core` | 数据形状：Source、Interpreter / InterpretSession、Recognizer / Translator / Synthesizer、DubManifest、能力表、错误分类 |
| `provider/doubao`、`provider/gemini`、`provider/openai` | 每家一个包，导出能力表和 doctor 检查；豆包另有配音用的识别、翻译、合成客户端（含重试和用量统计） |
| `provider/registry` | 内置厂商列表。接入新厂商 = 新包 + 这里一行 |
| `source` | 用 yt-dlp、ffprobe 把链接解析成 Source |
| `transcript` | 把平台字幕（vtt / srt / json3）拼成句子 |
| `dub` | 预先配音：manifest 续跑、对齐、混音、双语 SRT |
| `media` | 调用系统 ffmpeg / ffprobe / yt-dlp |
| `config` | 读 Key |
| `cli`、`cmd/voxbridge` | 命令行 |

## 版权与使用责任

VoxBridge 只是在你自己的电脑上处理你提供的链接。**你要对所处理内容的版权和使用方式负责**，请确认你有权收听、转写、翻译和保存这些内容，并遵守来源平台的服务条款。

VoxBridge 不绕过 DRM，不做登录抓取，不批量处理整个频道。

## 许可证

[Apache-2.0](LICENSE)
