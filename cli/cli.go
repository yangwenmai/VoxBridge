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
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "不认识的命令 %q\n\n%s", args[0], usage)
	return 2
}
