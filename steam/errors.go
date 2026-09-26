package steam

import "errors"

var ErrOverlayLoadLaunchedViaSteam = errors.New("launched via Steam, overlay should already be loaded")
var ErrSteamNotRunning = errors.New("Steam is not running, loading overlay is useless") //nolint
var ErrMarkerNotFound = errors.New("SISR marker shortcut not found in Steam shortcuts") //nolint
var ErrShortcutsVDFNotFound = errors.New("shortcuts.vdf does not exist")
var ErrOverlayUnsupportedArch = errors.New("Steam only ships an x64 GameOverlayRenderer64.dll, which cannot be loaded into a native Windows ARM64 process; use the windows_x64 build of SISR (it runs under x64 emulation on Windows on ARM)") //nolint
