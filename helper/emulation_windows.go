//go:build windows

package helper

import (
	"sync"

	"golang.org/x/sys/windows"
)

const imageFileMachineARM64 = 0xAA64

var emulatedOnARM64 = sync.OnceValue(func() bool {
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return false
	}
	return nativeMachine == imageFileMachineARM64
})

// RunningOnARM64Windows reports whether the host is Windows on ARM, which
// for SISR's x64 build means it is running under x64 emulation.
func RunningOnARM64Windows() bool {
	return emulatedOnARM64()
}
