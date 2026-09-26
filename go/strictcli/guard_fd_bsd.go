//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package strictcli

import "syscall"

func dupTo(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }
