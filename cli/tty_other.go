//go:build !darwin && !linux

package cli

import "os"

// isTerminal is false where VoxBridge cannot tell, so a run without --asr
// stops instead of prompting.
func isTerminal(*os.File) bool { return false }
