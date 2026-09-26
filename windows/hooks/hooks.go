//go:build !windows

package hooks

func Supported() bool { return false }

func EnumerateExports(dllName string) map[string][16]byte { return map[string][16]byte{} }

func DetectHooks(dllName string) []string { return nil }

func ExportAddr(dllName, exportName string) (uintptr, bool) { return 0, false }

func ExportBaseline16(dllName, exportName string) ([16]byte, bool) { return [16]byte{}, false }

func IsJmp(addr uintptr) bool { return false }

func RestoreBaseline(addr uintptr, baseline [16]byte) error { return ErrNotSupported }

func Unhook(dllName, exportName string) bool { return false }

func DetectHIDHooks() []string { return nil }

func UnhookSteamHIDHooks() []string { return nil }
