//go:build !windows

package main

import "syscall"

// detachedSysProcAttr starts the child in a new session (setsid) so it is not
// killed by a SIGHUP when the parent hook process exits.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
