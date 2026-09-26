//go:build !windows

package main

import (
	"github.com/alecthomas/kong"

	"github.com/Alia5/SISR/config"
)

func applyPlatformStartup(_ config.Global) {}

func platformDefaults() kong.Resolver {
	return kong.ResolverFunc(func(*kong.Context, *kong.Path, *kong.Flag) (any, error) { return nil, nil })
}
