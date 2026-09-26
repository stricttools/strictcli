//go:build linux

package strictcli

import "syscall"

func dupTo(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
