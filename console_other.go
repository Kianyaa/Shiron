//go:build !windows

package main

// enableVT: Linux and macOS terminals understand colors already.
func enableVT() bool { return true }
