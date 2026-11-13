// Package doubao adapts Volcengine Doubao Speech: simultaneous
// interpretation 2.0, recorded-file recognition 2.0, machine translation
// and TTS 2.0, all on one API key.
package doubao

import "github.com/yangwenmai/voxbridge/core"

const (
	ID      = "doubao"
	KeyEnv  = "DOUBAO_API_KEY"
	host    = "openspeech.bytedance.com"
	astURL  = "wss://" + host + "/api/v4/ast/v2/translate"
	mtURL   = "https://" + host + "/api/v3/machine_translation/matx_translate"
	ttsURL  = "https://" + host + "/api/v3/tts/unidirectional"
	astRes  = "volc.service_type.10053"
	mtRes   = "volc.speech.mt"
	ttsRes  = "seed-tts-2.0"
	success = 20000000

	// TranslateBatchLimit is the most texts one translation request takes.
	TranslateBatchLimit = 16
)

func langs(tags ...string) []core.Lang {
	out := make([]core.Lang, len(tags))
	for i, t := range tags {
		out[i] = core.Lang(t)
	}
	return out
}

// lang20 is the AST "lang_20" set; yue-CN and sh-CN are source-only dialects.
var lang20 = langs("zh", "en", "de", "fr", "es", "id", "ja", "pt", "ko", "tr",
	"ms", "nl", "ro", "pl", "cs", "ar", "th", "vi", "ru", "it")

var astSources = append(append([]core.Lang{}, lang20...), "yue-CN", "sh-CN")

var mtLangs = langs("zh", "zh-Hant", "en", "ja", "ko", "fr", "de", "es", "pt", "ru",
	"ar", "it", "nl", "pl", "ro", "sv", "da", "nb", "fi", "hu", "cs", "hr", "el",
	"he", "tr", "uk", "th", "vi", "id", "ms", "tl", "hi")

// ASTVoices are the only stock voices interpretation accepts. Without one
// the service clones the speaker's voice, which VoxBridge does not do.
var ASTVoices = []core.Voice{
	{ID: "zh_female_vv_uranus_bigtts", Name: "Vivi 2.0"},
	{ID: "zh_male_jingqiangkanye_emo_mars_bigtts"},
}

// TTSVoices are the stock TTS 2.0 voices handed to speakers in turn.
var TTSVoices = []core.Voice{
	{ID: "zh_female_vv_uranus_bigtts", Name: "Vivi 2.0"},
	{ID: "zh_male_m191_uranus_bigtts", Name: "云舟 2.0"},
	{ID: "zh_female_xiaohe_uranus_bigtts", Name: "小何 2.0"},
	{ID: "zh_male_taocheng_uranus_bigtts", Name: "小天 2.0"},
	{ID: "zh_male_liufei_uranus_bigtts", Name: "刘飞 2.0"},
	{ID: "zh_female_sophie_uranus_bigtts", Name: "魅力苏菲 2.0"},
}

var capabilities = core.Capabilities{
	Provider: ID,
	Services: []core.Service{
		{
			Capability: core.CapInterpret,
			Model:      astRes,
			// With a stock voice the target must be Chinese or English.
			Languages:        []core.LangRule{{From: astSources, To: langs("zh", "en")}},
			Voices:           ASTVoices,
			Subtitles:        true,
			InputSampleRate:  16000,
			OutputSampleRate: 16000,
			Pricing: core.Pricing{
				Unit: "token", Currency: "CNY", PerHour: 21,
				Basis: "输入 80 元/百万 token（约 6.25 token/秒），输出音频 300 元/百万 token（约 25 token/秒），按译音时长约为原声 70% 估算",
			},
		},
		{
			Capability: core.CapRecognize,
			Model:      asrRes,
			// The service also handles dialects and other languages, but
			// only Chinese and English are listed by name; VoxBridge sends
			// no language hint.
			Languages:       []core.LangRule{{From: langs("zh", "en"), To: langs("zh", "en")}},
			InputSampleRate: asrSampleRate,
			Pricing: core.Pricing{
				Unit: "hour", Currency: "CNY", PerHour: 0.8,
				Basis: "豆包录音文件识别模型 2.0 0.8 元/小时",
			},
		},
		{
			Capability: core.CapTranslate,
			Model:      mtRes,
			Languages:  []core.LangRule{{From: append([]core.Lang{core.AnyLang}, mtLangs...), To: mtLangs}},
			Pricing: core.Pricing{
				Unit: "token", Currency: "CNY", PerHour: 0.1,
				Basis: "输入 1.8 元/百万 token，输出 5.4 元/百万 token；一小时英文约 1.5 万 token",
			},
		},
		{
			Capability:       core.CapSynthesize,
			Model:            ttsRes,
			Languages:        []core.LangRule{{From: langs("zh"), To: langs("zh")}},
			Voices:           TTSVoices,
			OutputSampleRate: 24000,
			Pricing: core.Pricing{
				Unit: "character", Currency: "CNY", PerHour: 5,
				Basis: "3 元/万字符；一小时英文译成中文约 1.5 万到 1.8 万字",
			},
		},
	},
}
