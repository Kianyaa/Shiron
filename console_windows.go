package main

import "golang.org/x/sys/windows"

// enableVT turns on color support in the classic Windows console (Windows Terminal
// already has it). It reports false if the console can't do colors.
func enableVT() bool {
	h := windows.Handle(windows.Stdout)
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
