// +build !linux

package main

import (
        "syscall"
)

func Dup2(oldfd int, newfd int) (err error) {
	return syscall.Dup2(oldfd, newfd)
}

