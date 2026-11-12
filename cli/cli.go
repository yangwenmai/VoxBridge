// Package cli implements the voxbridge command.
package cli

import (
	"context"
	"fmt"
	"io"
)

const usage = `voxbridge — 给链接就能听懂

用法:
  voxbridge doctor              检查 ffmpeg、yt-dlp 和各厂商 API Key 能用哪些能力
  voxbridge parse <链接>        把链接解析成直播或点播来源
      --json                    输出 JSON
      --sample <file.wav>       另存前若干秒解码后的音频（16 kHz 单声道），用来试听
      --seconds <n>             --sample 的秒数，默认 10
  voxbridge dub <链接>          点播预先配音：整集翻译、合成中文，输出混好的音频和双语字幕
      --out <目录>              工作目录，默认 ./voxbridge-dubs/<链接哈希>；中断后用同一目录接着做
      --asr                     不用平台字幕，改用语音识别
      --original-volume <v>     原声音量，0 到 1，默认 0.25（中文叠在压低的原声上）

Key 从环境变量读取（DOUBAO_API_KEY、GEMINI_API_KEY、OPENAI_API_KEY），
或从 ~/.config/voxbridge/config.json 读取，环境变量优先。
`

// Run executes args and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "doctor":
		return doctor(ctx, stdout, stderr)
	case "parse":
		return parse(ctx, args[1:], stdout, stderr)
	case "dub":
		return dubCmd(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "不认识的命令 %q\n\n%s", args[0], usage)
	return 2
}
