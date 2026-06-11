//go:build !linux

package main

// On non-Linux (dev machines) we can't read /proc; metrics are zero. The real
// target is NixOS/Linux where system_linux.go provides the values.

func systemMetrics() sysMetrics { return sysMetrics{} }
