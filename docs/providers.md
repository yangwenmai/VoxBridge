# 厂商能力与价格

2026-11-12 对照各家官方文档核对。和项目规格里的笔记有出入的地方标了 **⚠ 与规格不同**，代码以文档为准。价格是公开价，没对过账单；每小时费用是估算。

代码里的能力表在各 provider 包的 `Capabilities()`（`provider/doubao`、`provider/gemini`、`provider/openai`），业务代码只读能力表，不判断厂商名。

## 总览

| 厂商 | 同传模型 | 目标语言 | 选音色 | 字幕 | 输入 / 输出采样率 | 公开价 | 每小时估算 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 豆包 | 同声传译 2.0（`volc.service_type.10053`） | 指定音色时只能中、英 | 2 个公版音色 | 有 | 16 kHz / 16 kHz | 输入 80 元、输出音频 300 元每百万 token | 约 21 元 |
| Gemini | `gemini-3.5-live-translate-preview` | 78 种语言 | 不能选，模型复刻讲者声音 | 有 | 16 kHz / 24 kHz | 输入 3.50、输出 21.00 美元每百万 token | 约 2.21 美元 |
| OpenAI | `gpt-realtime-translate` | 13 种 | 不能选 | 有 | 24 kHz / 24 kHz | 0.034 美元每分钟 | 约 2.04 美元 |

延迟数字都来自规格里的实测或第三方评测，官方文档没有给出可比数字，见各节。里程碑 5 会用同一素材实测三家。

## 豆包语音（火山引擎）

一个 API Key 走三项服务，host `openspeech.bytedance.com`，请求头 `X-Api-Key`。控制台「豆包语音 → 开通管理」里要分别开通。新账号各项有半年免费额度：同传 100 万 token，机器翻译 100 万 token，语音合成 2.0 两万字符。

### 同声传译 2.0

- 文档：[同声传译 2.0 API](https://docs.volcengine.com/docs/DoubaoVoice/SimultaneousInterpretation20APIAccessDocumentation?lang=zh)
- `wss://openspeech.bytedance.com/api/v4/ast/v2/translate`，请求头 `X-Api-Key`、`X-Api-Resource-Id: volc.service_type.10053`、`X-Api-Connect-Id`。消息是 protobuf，官方附 `protos.tar.gz`。这些和规格一致。
- 事件号与规格一致，另外还有：SourceSubtitleStart 650、SourceSubtitleResponse 651、SourceSubtitleEnd 652（原文字幕）、TranslationSubtitleStart 653、TranslationSubtitleResponse 654、TTSSentenceStart 350、TTSSentenceEnd 351、AudioMuted 250（静音 2 秒后开始每秒报一次）。
- 输入：wav 容器、raw 编码、16000 Hz、16 bit、单声道，建议 80 ms 一包。与规格一致。
- 输出：`target_audio.rate` 可选 16000 或 24000。**pcm 在 16000 Hz 下是 16 bit 整型，在 24000 Hz（默认值）下是 32 位浮点**。规格写的 16000 / 16 bit 没问题，但不能只改采样率。
- 语言，**⚠ 与规格不同**（规格只说了要填公版音色）：
  - S2S 指定音色：源语言是 20 种（lang_20）加粤语、上海话，**目标语言只能是中文或英文**。
  - S2S 不传音色会复刻讲者声音：源、目标都在 8 种（lang_8）内，且其中一边必须是中文或英文。VoxBridge 第一版不做复刻，所以不走这条。
  - S2T（只出字幕）：20 种互译，一边必须是中英。
  - lang_20：zh、en、de、fr、es、id、ja、pt、ko、tr、ms、nl、ro、pl、cs、ar、th、vi、ru、it。另有 `zhen` 中英反转互译。
- 音色，**⚠ 与规格不同**：同传只接受 2 个公版音色，`zh_female_vv_uranus_bigtts` 和 `zh_male_jingqiangkanye_emo_mars_bigtts`。规格列的 6 个 `*_uranus_bigtts` 是语音合成 2.0 的音色，只能用于预先配音。传错或不传都会退回复刻讲者声音。
- 错误码与规格一致（45000001、45000002、45000081、45000151、55000031）。
- 价格（[推理价格](https://docs.volcengine.com/docs/doubao-voice/billing-instructions?lang=zh)）：输入 80 元每百万 token，输出文本 80 元，输出音频 300 元。官方折算：输入音频约 6.25 token 每秒，输出音频约 25 token 每秒。估算一小时英文：输入 22,500 token 约 1.8 元，译音按原声时长七成算约 63,000 token 约 18.9 元，合计约 21 元。资源包最低 56 元每百万 token。
- 延迟：官方没有给 API 层的延迟数字。规格里 CastMind 实测中文固定晚 2 到 3 秒。

### 机器翻译

- 文档：[机器翻译](https://docs.volcengine.com/docs/doubao-voice/machine-translation-large-model-api-access-documentation?lang=zh)
- `POST /api/v3/machine_translation/matx_translate`，resource `volc.speech.mt`，每次最多 16 条，单条不超过 1024 token。与规格一致。
- 请求头还要带 `X-Api-Request-Id`（文档写「推荐」，**⚠ 实测不带会返回 HTTP 500、55000000 `missing X-Api-Request-Id`**）。
- `source_language` 可省略，自动检测。`detected_source_language` **只在没传 `source_language` 时返回**。
- 支持 32 种语言互译：zh、zh-Hant、en、ja、ko、fr、de、es、pt、ru、ar、it、nl、pl、ro、sv、da、nb、fi、hu、cs、hr、el、he、tr、uk、th、vi、id、ms、tl、hi。
- 超长返回 45000130。
- 用量在每条译文里：`translation_list[].usage.{prompt_tokens,completion_tokens}`（2026-11-12 实测，顶层没有 `usage`）。录下的响应在 `provider/doubao/testdata/mt_ok.json`。
- 价格：输入 1.8 元、输出 5.4 元每百万 token。一小时英文约一万多 token，不到 0.1 元。

### 语音合成 2.0

- 文档：[HTTP 单向流式合成](https://docs.volcengine.com/docs/doubao-voice/unidirectional-streaming-text-to-speech-http?lang=zh)、[音色列表](https://docs.volcengine.com/docs/doubao-voice/tone-list-3?lang=zh)
- `POST /api/v3/tts/unidirectional`，resource `seed-tts-2.0`，请求体与规格一致。返回多段 JSON 首尾相接，每段 `data` 是 base64 音频片，**最后一段 `code` 为 20000000 表示结束**。
- 规格列的 6 个音色都在官方 2.0 音色表里：Vivi 2.0、云舟 2.0、小何 2.0、小天 2.0、刘飞 2.0、魅力苏菲 2.0。
- 价格：3 元每万字符（资源包最低 2.1 元）。一小时英文译成中文约 1.5 万到 1.8 万字，约 4.5 到 5.4 元。加上机器翻译，和规格「一小时 5 到 6 元」一致；同传约 21 元，约为配音的四倍，也和规格一致（都是估算）。

### 录音文件识别 2.0（预先配音没有平台字幕时用）

- 文档：[录音文件识别简介](https://docs.volcengine.com/docs/doubao-voice/access-must-read-1?lang=zh)，[计费](https://docs.volcengine.com/docs/doubao-voice/billing-instructions?lang=zh)，[新版控制台指引](https://docs.volcengine.com/docs/doubao-voice/console-guidance?lang=zh)
- 控制台产品名和 resource id 的对应：
  - 豆包录音文件识别模型 2.0 标准版：`volc.seedasr.auc`。**VoxBridge 用这个。**
  - 豆包录音文件识别模型 1.0 标准版：`volc.bigasr.auc`。
  - 1.0 极速版：`volc.bigasr.auc_turbo`。1.0 闲时版：`volc.bigasr.auc_idle`。**⚠ 这两个已从控制台下线**，存量用户还能用，新用户要找技术支持开通。
- 2026-11-13 用 Mai 的 Key 实测，只有 `volc.seedasr.auc` 有权限，其余都返回 403、`45000030` requested resource not granted。
- 开通、API Key 和资源包都绑定在项目上，项目之间相互隔离。开通步骤是：在控制台左上角切到 Key 所在的项目，进「开通管理」，找到模型点「开通」。
- 两步走：
  - `POST /api/v3/auc/bigmodel/submit` 提交任务，任务 id 由调用方生成，放在 `X-Api-Request-Id`。
  - `POST /api/v3/auc/bigmodel/query` 查询，用同一个 id，请求体是 `{}`。
  - 状态在**响应头** `X-Api-Status-Code`：20000000 完成，20000001 处理中，20000002 排队中，20000003 静音，45000092 任务不存在。消息在 `X-Api-Message`。
  - 录下的响应在 `provider/doubao/testdata/asr_*.json`。
- **⚠ 文档没写的用法**：文档里 `audio` 只有 `url`（公网可访问的链接）。实测 `audio.data` 放 base64 mp3（同时传 `format: "mp3"`）也能用，VoxBridge 用的就是这种方式，因为本机 CLI 没有公网 URL。内联大小的上限不知道。实测一次传 34.6 分钟（16 kHz 单声道 24 kbps mp3，约 6 MB，base64 后约 8 MB）可以。保险起见，VoxBridge 按 1 小时一段提交。超过 1 小时时，每段的说话人编号各自独立，不跨段对应（**未验证**：长于 1 小时的整集还没实测）。如果以后官方不再接受 `data`，就只能先把音频上传到对象存储，再提交 URL。
- 续跑：
  - 任务 id 在提交**之前**就写进工作目录的 `asr-tasks.json`。
  - 续跑时，已经提交的任务只查询，不重提。
  - 存了 id 但不确定有没有提交成功的，先查一次：查得到就接着等；返回 45000092（服务端没有这个任务）才用同一个 id 补交。所以不会为同一段音频付两次钱。
  - 已经提交过、却查不到的（多半是过期了），会停下来提示，删掉 `asr-tasks.json` 才会重新提交并再次计费。
  - 识别结果也存进这个文件，之后再跑不再发请求。
- 说话人：开 `enable_speaker_info`、不指定语言时，英文也会返回标签，在 `utterances[].additions.speaker`（"1"、"2"…）。
- 价格：后付费 0.8 元/小时（资源包最低约 0.67 元/小时）。之前用的极速版是 4.5 元/小时。
- 等待时间：官方说「3 小时内返回」。实测 5 秒的音频约 2 秒返回，34.6 分钟的播客从提交到完成约 2 分 30 秒。

### 没有字幕的播客实测（2026-11-13，Planet Money「How surveillance crept up on us」，34:36）

| 步骤 | 请求 | 估算费用 |
| --- | --- | --- |
| 提交识别后约 10 秒按 Ctrl-C | 识别：1 次提交、1 次查询 | 0.46 元 |
| 不加 `--asr` 续跑（已提交过，不再询问） | 识别：0 次提交、26 次查询（等了 2 分 10 秒）；翻译 24 次；合成 377 次，11452 字符 | 3.52 元 |
| 再跑一次 | 0 次 | 0 元 |

合计约 3.98 元，按公开价估算，没对账单。识别出 377 句、7 位说话人。音色只有 6 个，所以说话最少的那位和说话最多的那位共用 Vivi 2.0。输出 36:32，比原声长 1 分 55 秒（原声在句首等中文时插入的静音）。

### 实测的错误响应（2026-11-12，用错误的 Key）

| 服务 | HTTP | 响应体 |
| --- | --- | --- |
| 机器翻译 | 500 | `{"code":55000000,"message":"Invalid X-Api-Key"}` |
| 语音合成 2.0 | 401 | `{"header":{"code":45000010,"message":"Invalid X-Api-Key",...}}` |
| 同传 WebSocket 握手 | 401 | `{"error":"Invalid X-Api-Key"}` |

### 预先配音实测（2026-11-12，有人工英文字幕的 TED 视频）

| 视频 | 时长 | 句子 | 机器翻译 | 语音合成 2.0 | 估算费用 |
| --- | --- | --- | --- | --- | --- |
| Grit（H14bBuluwB8） | 6:13 | 56（1 句跳过） | 4 次请求 | 55 次请求，1596 字符 | 约 0.50 元 |
| 10 mindful minutes（qzR62JJCMBQ），中途 Ctrl-C 后续跑 | 9:24 | 95（1 句跳过） | 6 次请求（续跑 0 次） | 33 + 63 次请求，2717 字符 | 约 0.84 元 |

合成字数折算约每小时 1.7 万字、5.2 元，和上面的估算一致。估算按公开价，没对账单。

**⚠ 与规格不同**：规格说「HTTP 500 加 55000000」表示服务没开通。实测 Key 本身错了也是 500 加 55000000，要看 message 区分。VoxBridge 先判 `Invalid X-Api-Key`（Key 错），再判 `mismatched` 等词（没开通）。三种响应都带 `X-Tt-Logid`。

## Gemini

- 文档：[Live translate](https://aistudio.google.com/docs/live-api/live-translate)、[模型页](https://aistudio.google.com/docs/models/gemini-3.5-live-translate-preview)、[价格](https://ai.google.dev/gemini-api/docs/pricing)
- 模型名 `gemini-3.5-live-translate-preview`，与规格一致。公开预览版。
- 连接：Live API WebSocket `wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent`，setup 里 `generationConfig.translationConfig.targetLanguageCode` 设目标语言，`inputAudioTranscription` / `outputAudioTranscription` 打开原文和译文字幕。不需要指定源语言。**⚠ Live translate 指南的示例把两个 transcription 字段写在 `generationConfig` 里，服务端会拒绝（`Unknown name "inputAudioTranscription" at 'setup.generation_config'`）**；按 [API 参考](https://ai.google.dev/api/live#BidiGenerateContentSetup) 它们直接放在 `setup` 下。
- 输入 16 kHz 16 bit 单声道 PCM，建议 100 ms 一块；输出 24 kHz 16 bit PCM。与规格里 AIE live-translate 的推断一致。
- 只收音频，不支持文字输入、工具和指令。不能选音色，模型会复刻讲者声音，官方说长停顿后声音可能漂移、性别判断可能出错。
- 语言：官方表列出 78 种语言（79 个代码，挪威语有 `no`、`nb` 两个），中文是 `zh-Hans` / `zh-Hant`，不是 `zh`。规格说「70 多种」，一致。
- 价格：免费层免费（数据会被用于改进产品）；付费层输入 3.50 美元、输出 21.00 美元每百万音频 token，按 25 token 每秒折合约 0.0368 美元每分钟，即每小时约 2.21 美元。规格写约 2.2 美元，一致。
- 延迟：官方没有给数字。规格里的「约落后 3 秒，偶尔 13.9 秒」来自第三方评测，未验证。

## OpenAI

- 文档：[模型页](https://developers.openai.com/api/docs/models/gpt-realtime-translate)、[Realtime translation 指南](https://developers.openai.com/api/docs/guides/realtime-translation)、[价格](https://developers.openai.com/api/docs/pricing)
- 模型名 `gpt-realtime-translate`，与规格一致。只支持专用端点 `wss://api.openai.com/v1/realtime/translations?model=gpt-realtime-translate`，不走普通 `/v1/realtime`。
- 连上后用 `session.update` 设 `session.audio.output.language`；用 `session.input_audio_buffer.append` 发 base64 的 **24 kHz** PCM16 单声道；收 `session.output_audio.delta`（24 kHz）、`session.output_transcript.delta`、`session.input_transcript.delta`；结束发 `session.close` 等 `session.closed`。原文字幕要另配 `gpt-realtime-whisper`。
- 不支持自定义提示，也不支持选音色。每种目标语言要开一个会话。
- 语言：输入 70 多种，自动检测；目标 13 种：es、pt、fr、ja、ru、zh、de、ko、hi、id、vi、it、en。规格说 13 种含中文，一致。
- 价格：按输入音频时长 0.034 美元每分钟，每小时约 2.04 美元。规格写约 2.0 美元，一致。
- 延迟：官方没有给数字。规格里「首个译音约 0.7 秒，整句约 3.8 秒」来自第三方评测，未验证。
- **⚠ 注意**：Key 错误时 OpenAI 会在 message 里回显 Key（实测把整个错误 Key 原样回显）。VoxBridge 输出前会把它替换成 `[REDACTED]`。

## doctor 怎么检查

`voxbridge doctor` 对每个配置了 Key 的厂商发最小请求，不重试：

| 厂商 | 请求 | 计费 |
| --- | --- | --- |
| 豆包 | 机器翻译一句 `Hello.`；合成「你好。」；同传只做 WebSocket 握手，不开会话 | 约 10 token 加 3 个字符，在免费额度内 |
| Gemini | 读模型信息；开 Live 会话发 setup，等到 `setupComplete` 就关 | 不发音频，不计费 |
| OpenAI | 读模型信息；开翻译会话，等到 `session.created` 就关 | 不发音频，不计费 |

豆包同传的握手只证明 Key 能连上，不能证明语言对和音色可用，这要到里程碑 3 开真实会话才知道。
