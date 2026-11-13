//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal asks the kernel for terminal settings; /dev/null and pipes
// have none even though /dev/null is a character device.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(getTermios), uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
