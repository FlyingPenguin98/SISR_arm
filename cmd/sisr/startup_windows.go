//go:build windows

package main

import (
	"os"

	"github.com/alecthomas/kong"

	"github.com/Alia5/SISR/config"
	"github.com/Alia5/SISR/helper"
	"github.com/Alia5/SISR/windows"
)

func applyPlatformStartup(cfg config.Global) {
	if cfg.Console {
		return
	}

	if windows.IsRunFromGUI() {
		windows.HideConsoleWindow()
	}
}

// platformDefaults overrides built-in defaults that do not work on this
// machine. It only applies when a flag was not set on the command line, via
// its env var, or in a config file.
func platformDefaults() kong.Resolver {
	return kong.ResolverFunc(func(_ *kong.Context, _ *kong.Path, flag *kong.Flag) (any, error) {
		for _, env := range flag.Tag.Envs {
			if _, ok := os.LookupEnv(env); ok {
				return nil, nil
			}
		}
		// Under x64 emulation on Windows on ARM the transparent fullscreen
		// overlay is not composited as transparent and covers the screen in black.
		if flag.Name == "window.fullscreen" && helper.RunningOnARM64Windows() {
			return false, nil
		}
		return nil, nil
	})
}
