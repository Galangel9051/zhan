//go:build unix

package aiops

import "syscall"

const nonblock = syscall.O_NONBLOCK
