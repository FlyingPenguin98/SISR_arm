//go:build windows

package hooks

import (
	"encoding/binary"
	"log/slog"
	"os"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibraryW      = kernel32.NewProc("LoadLibraryW")
	procGetModuleFileName = kernel32.NewProc("GetModuleFileNameW")
	procVirtualProtect    = kernel32.NewProc("VirtualProtect")
)

const (
	peSignature          = 0x00004550
	pe32Magic            = 0x10b
	pe32PlusMagic        = 0x20b
	pageExecuteReadWrite = uint32(0x40)
)

type peSection struct {
	virtualAddress uint32
	virtualSize    uint32
	rawPointer     uint32
	rawSize        uint32
}

const imageFileMachineARM64 = 0xAA64

var emulatedOnARM64 = sync.OnceValue(func() bool {
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return false
	}
	return nativeMachine == imageFileMachineARM64
})

// Supported reports whether hook detection/removal can work in this process.
//
// On Windows on ARM, system DLLs are ARM64X binaries whose export table is
// swapped in memory for emulated x64 processes, so the on-disk baseline does
// not match the code x64 callers execute. Restoring it corrupts the export
// and crashes on the next call.
func Supported() bool {
	return !emulatedOnARM64()
}

var (
	baselineOnce  sync.Map // map[string]*sync.Once
	baselineCache sync.Map // map[string]map[string][16]byte
)

// EnumerateExports returns the on-disk first-16-byte snapshots for every named
// export of dllName. Results are cached after the first call.
func EnumerateExports(dllName string) map[string][16]byte {
	onceVal, _ := baselineOnce.LoadOrStore(dllName, &sync.Once{})
	onceVal.(*sync.Once).Do(func() {
		module, err := loadModule(dllName)
		if err != nil {
			slog.Error("Failed to load DLL", "dll", dllName, "error", err)
			baselineCache.Store(dllName, map[string][16]byte{})
			return
		}
		path, err := modulePath(module)
		if err != nil {
			slog.Error("Failed to resolve DLL path", "dll", dllName, "error", err)
			baselineCache.Store(dllName, map[string][16]byte{})
			return
		}
		baseline, err := enumerateExportsFromDisk(path)
		if err != nil {
			slog.Error("Failed to enumerate DLL exports from disk", "dll", dllName, "error", err)
			baselineCache.Store(dllName, map[string][16]byte{})
			return
		}
		baselineCache.Store(dllName, baseline)
	})
	v, _ := baselineCache.Load(dllName)
	if v == nil {
		return map[string][16]byte{}
	}
	return v.(map[string][16]byte)
}

// DetectHooks returns names of exports in dllName whose in-memory first 16 bytes
// differ from the on-disk baseline.
func DetectHooks(dllName string) []string {
	if !Supported() {
		return nil
	}
	baseline := EnumerateExports(dllName)
	if len(baseline) == 0 {
		return nil
	}
	loaded, err := enumerateLoadedExports(dllName)
	if err != nil {
		slog.Error("Failed to enumerate loaded exports", "dll", dllName, "error", err)
		out := make([]string, 0, len(baseline))
		for name := range baseline {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	var hooked []string
	for name, base16 := range baseline {
		addr, ok := loaded[name]
		if !ok || addr == 0 {
			continue
		}
		var cur [16]byte
		copy(cur[:], unsafe.Slice((*byte)(unsafe.Pointer(addr)), 16))
		if cur != base16 {
			hooked = append(hooked, name)
		}
	}
	sort.Strings(hooked)
	return hooked
}

// ExportAddr returns the in-memory address of a named export in dllName.
func ExportAddr(dllName, exportName string) (uintptr, bool) {
	loaded, err := enumerateLoadedExports(dllName)
	if err != nil {
		slog.Error("Failed to enumerate loaded exports", "dll", dllName, "error", err)
		return 0, false
	}
	addr, ok := loaded[exportName]
	if !ok || addr == 0 {
		return 0, false
	}
	return addr, true
}

// ExportBaseline16 returns the on-disk first 16 bytes for exportName in dllName.
func ExportBaseline16(dllName, exportName string) ([16]byte, bool) {
	v, ok := EnumerateExports(dllName)[exportName]
	return v, ok
}

// IsJmp reports whether the bytes at addr match a known JMP trampoline:
// E9 rel32, FF 25 disp32, or 48 B8 imm64 / FF E0.
func IsJmp(addr uintptr) bool {
	b := unsafe.Slice((*byte)(unsafe.Pointer(addr)), 16)
	switch {
	case b[0] == 0xE9:
		return true
	case b[0] == 0xFF && b[1] == 0x25:
		return true
	case b[0] == 0x48 && b[1] == 0xB8 && b[10] == 0xFF && b[11] == 0xE0:
		return true
	}
	return false
}

// RestoreBaseline overwrites addr with baseline bytes, temporarily granting
// PAGE_EXECUTE_READWRITE via VirtualProtect.
func RestoreBaseline(addr uintptr, baseline [16]byte) error {
	if !Supported() {
		return ErrNotSupported
	}
	var oldProtect uint32
	ret, _, callErr := procVirtualProtect.Call(
		addr,
		uintptr(len(baseline)),
		uintptr(pageExecuteReadWrite),
		uintptr(unsafe.Pointer(&oldProtect)),
	)
	if ret == 0 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return ErrVirtualProtectFailed
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(addr)), len(baseline)), baseline[:])
	var tmp uint32
	if ret2, _, restoreErr := procVirtualProtect.Call(addr, uintptr(len(baseline)), uintptr(oldProtect), uintptr(unsafe.Pointer(&tmp))); ret2 == 0 {
		if restoreErr != syscall.Errno(0) {
			return restoreErr
		}
		return ErrVirtualProtectFailed
	}
	return nil
}

// Unhook checks whether exportName in dllName carries a JMP hook and,
// if so, restores the on-disk baseline bytes. Returns true when unhooked.
func Unhook(dllName, exportName string) bool {
	if !Supported() {
		return false
	}
	addr, ok := ExportAddr(dllName, exportName)
	if !ok {
		slog.Error("export not found", "dll", dllName, "export", exportName)
		return false
	}
	if !IsJmp(addr) {
		return false
	}
	baseline, ok := ExportBaseline16(dllName, exportName)
	if !ok {
		slog.Error("no baseline for export", "dll", dllName, "export", exportName)
		return false
	}
	if err := RestoreBaseline(addr, baseline); err != nil {
		slog.Error("failed to restore baseline", "dll", dllName, "export", exportName, "error", err)
		return false
	}
	slog.Info("removed hook", "dll", dllName, "export", exportName)
	return true
}

func loadModule(dllName string) (uintptr, error) {
	n, err := syscall.UTF16PtrFromString(dllName)
	if err != nil {
		return 0, err
	}
	module, _, callErr := procLoadLibraryW.Call(uintptr(unsafe.Pointer(n)))
	if module == 0 {
		if callErr != syscall.Errno(0) {
			return 0, callErr
		}
		return 0, ErrLoadLibraryWFailed
	}
	return module, nil
}

func modulePath(module uintptr) (string, error) {
	buf := make([]uint16, 32768)
	n, _, callErr := procGetModuleFileName.Call(
		module,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", ErrGetModuleFileNameWFailed
	}
	return syscall.UTF16ToString(buf[:n]), nil
}

func enumerateExportsFromDisk(path string) (map[string][16]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	eLfanew, ok := readU32LE(data, 0x3c)
	if !ok {
		return nil, ErrInvalidPEMissingELfanew
	}
	ntOff := int(eLfanew)
	sig, ok := readU32LE(data, ntOff)
	if !ok || sig != peSignature {
		return nil, ErrInvalidPESignature
	}

	fileHeaderOff := ntOff + 4
	numberOfSections, ok := readU16LE(data, fileHeaderOff+2)
	if !ok {
		return nil, ErrInvalidPESectionCount
	}
	sizeOfOptionalHeader, ok := readU16LE(data, fileHeaderOff+16)
	if !ok {
		return nil, ErrInvalidPEOptionalHeaderSize
	}

	optOff := fileHeaderOff + 20
	magic, ok := readU16LE(data, optOff)
	if !ok {
		return nil, ErrInvalidPEOptionalHeaderMagic
	}

	dataDirOff := 0
	switch magic {
	case pe32PlusMagic:
		dataDirOff = optOff + 0x70
	case pe32Magic:
		dataDirOff = optOff + 0x60
	default:
		return nil, ErrUnsupportedOptionalHeaderMagic
	}

	exportRVA, ok := readU32LE(data, dataDirOff)
	if !ok || exportRVA == 0 {
		return nil, ErrNoExportDirectory
	}

	sectionOff := optOff + int(sizeOfOptionalHeader)
	sections := make([]peSection, 0, int(numberOfSections))
	for i := range int(numberOfSections) {
		off := sectionOff + i*40
		virtualSize, ok := readU32LE(data, off+8)
		if !ok {
			return nil, ErrInvalidPESectionVirtualSize
		}
		virtualAddress, ok := readU32LE(data, off+12)
		if !ok {
			return nil, ErrInvalidPESectionVirtualAddress
		}
		rawSize, ok := readU32LE(data, off+16)
		if !ok {
			return nil, ErrInvalidPESectionRawSize
		}
		rawPtr, ok := readU32LE(data, off+20)
		if !ok {
			return nil, ErrInvalidPESectionRawPointer
		}
		sections = append(sections, peSection{
			virtualAddress: virtualAddress,
			virtualSize:    virtualSize,
			rawPointer:     rawPtr,
			rawSize:        rawSize,
		})
	}

	exportDirOff, ok := rvaToFileOffset(exportRVA, sections)
	if !ok {
		return nil, ErrFailedToMapExportDirectoryRVA
	}
	numberOfNames, ok := readU32LE(data, exportDirOff+24)
	if !ok {
		return nil, ErrInvalidExportDirectoryNumberOfNames
	}
	addressOfFunctions, ok := readU32LE(data, exportDirOff+28)
	if !ok {
		return nil, ErrInvalidExportDirectoryFunctions
	}
	addressOfNames, ok := readU32LE(data, exportDirOff+32)
	if !ok {
		return nil, ErrInvalidExportDirectoryNames
	}
	addressOfNameOrdinals, ok := readU32LE(data, exportDirOff+36)
	if !ok {
		return nil, ErrInvalidExportDirectoryOrdinals
	}

	namesOff, ok := rvaToFileOffset(addressOfNames, sections)
	if !ok {
		return nil, ErrFailedToMapNamesRVA
	}
	funcsOff, ok := rvaToFileOffset(addressOfFunctions, sections)
	if !ok {
		return nil, ErrFailedToMapFunctionsRVA
	}
	ordOff, ok := rvaToFileOffset(addressOfNameOrdinals, sections)
	if !ok {
		return nil, ErrFailedToMapOrdinalsRVA
	}

	exports := make(map[string][16]byte, int(numberOfNames))
	for i := range int(numberOfNames) {
		nameRVA, ok := readU32LE(data, namesOff+i*4)
		if !ok {
			continue
		}
		nameOff, ok := rvaToFileOffset(nameRVA, sections)
		if !ok {
			continue
		}
		name, ok := cStringAt(data, nameOff)
		if !ok || name == "" {
			continue
		}
		ordinal, ok := readU16LE(data, ordOff+i*2)
		if !ok {
			continue
		}
		funcRVA, ok := readU32LE(data, funcsOff+int(ordinal)*4)
		if !ok || funcRVA == 0 {
			continue
		}
		funcOff, ok := rvaToFileOffset(funcRVA, sections)
		if !ok {
			continue
		}
		if funcOff+16 > len(data) {
			continue
		}
		var first16 [16]byte
		copy(first16[:], data[funcOff:funcOff+16])
		exports[name] = first16
	}
	return exports, nil
}

func enumerateLoadedExports(dllName string) (map[string]uintptr, error) {
	base, err := loadModule(dllName)
	if err != nil {
		return nil, err
	}

	eLfanew := readMemU32(base + 0x3c)
	ntOff := base + uintptr(eLfanew)
	if readMemU32(ntOff) != peSignature {
		return nil, ErrInvalidLoadedPESignature
	}

	optOff := ntOff + 4 + 20
	magic := readMemU16(optOff)
	var dataDirOff uintptr
	switch magic {
	case pe32PlusMagic:
		dataDirOff = optOff + 0x70
	case pe32Magic:
		dataDirOff = optOff + 0x60
	default:
		return nil, ErrUnsupportedLoadedOptionalHeader
	}

	exportDirRVA := readMemU32(dataDirOff)
	if exportDirRVA == 0 {
		return nil, ErrLoadedModuleNoExportDirectory
	}

	exportDir := base + uintptr(exportDirRVA)
	numberOfNames := readMemU32(exportDir + 24)
	addressOfFunctions := readMemU32(exportDir + 28)
	addressOfNames := readMemU32(exportDir + 32)
	addressOfNameOrdinals := readMemU32(exportDir + 36)
	if numberOfNames == 0 || addressOfFunctions == 0 || addressOfNames == 0 || addressOfNameOrdinals == 0 {
		return nil, ErrInvalidLoadedExportDirectory
	}

	names := base + uintptr(addressOfNames)
	funcs := base + uintptr(addressOfFunctions)
	ords := base + uintptr(addressOfNameOrdinals)

	exports := make(map[string]uintptr, int(numberOfNames))
	for i := range numberOfNames {
		nameRVA := readMemU32(names + uintptr(i*4))
		if nameRVA == 0 {
			continue
		}
		name, ok := readCStringFromMemory(base+uintptr(nameRVA), 1024)
		if !ok || name == "" {
			continue
		}
		ordinal := readMemU16(ords + uintptr(i*2))
		funcRVA := readMemU32(funcs + uintptr(ordinal)*4)
		if funcRVA == 0 {
			continue
		}
		exports[name] = base + uintptr(funcRVA)
	}
	return exports, nil
}

func readU16LE(buf []byte, off int) (uint16, bool) {
	if off < 0 || off+2 > len(buf) {
		return 0, false
	}
	return binary.LittleEndian.Uint16(buf[off : off+2]), true
}

func readU32LE(buf []byte, off int) (uint32, bool) {
	if off < 0 || off+4 > len(buf) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(buf[off : off+4]), true
}

func cStringAt(buf []byte, off int) (string, bool) {
	if off < 0 || off >= len(buf) {
		return "", false
	}
	rest := buf[off:]
	for i, b := range rest {
		if b == 0 {
			return string(rest[:i]), true
		}
	}
	return "", false
}

func rvaToFileOffset(rva uint32, sections []peSection) (int, bool) {
	for _, s := range sections {
		span := max(s.rawSize, s.virtualSize)
		if rva >= s.virtualAddress && rva < s.virtualAddress+span {
			return int(s.rawPointer + rva - s.virtualAddress), true
		}
	}
	return 0, false
}

func readMemU16(addr uintptr) uint16 {
	return *(*uint16)(unsafe.Pointer(addr))
}

func readMemU32(addr uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(addr))
}

func readCStringFromMemory(addr uintptr, max int) (string, bool) {
	buf := make([]byte, 0, max)
	for i := range max {
		b := *(*byte)(unsafe.Pointer(addr + uintptr(i)))
		if b == 0 {
			return string(buf), true
		}
		buf = append(buf, b)
	}
	return "", false
}
