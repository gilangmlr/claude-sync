//go:build windows

package main

import "syscall"

// detachedSysProcAttr is a no-op on Windows; the child still runs independently
// once the parent returns without waiting on it.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
