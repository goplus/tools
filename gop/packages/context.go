// Copyright 2023 The GoPlus Authors (goplus.org). All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package packages

import (
	"go/types"
	"path/filepath"
	"sync"

	"github.com/goplus/mod/gopmod"
	"golang.org/x/tools/go/packages"
)

// Module provides module information for a package.
type Module = packages.Module

// A Context is an opaque packages.Load context.
// Contexts are safe for concurrent use.
type Context struct {
	// Types is an opaque type checking context. It may be used to share
	// identical type instances across type-checked packages or calls to
	// Instantiate. Contexts are safe for concurrent use.
	//
	// The use of a shared context does not guarantee that identical instances are
	// deduplicated in all cases.
	Types *types.Context

	mutex sync.Mutex
	mods  map[string]*gopmod.Module
}

// NewContext creates a new packages.Load context.
func NewContext(ctx *types.Context) *Context {
	mods := make(map[string]*gopmod.Module)
	return &Context{Types: ctx, mods: mods}
}

var (
	Default = NewContext(types.NewContext())
)

// LoadModFrom loads a Go+ module from gop.mod or go.mod file.
func (p *Context) LoadModFrom(gomod string) (ret *gopmod.Module, err error) {
	p.mutex.Lock()
	ret, ok := p.mods[gomod]
	p.mutex.Unlock()
	if ok {
		return ret, nil
	}
	if ret, err = loadModFrom(gomod); err == nil {
		p.mutex.Lock()
		p.mods[gomod] = ret
		p.mutex.Unlock()
	}
	return
}

// loadModFrom loads a Go+ module from gop.mod or go.mod file.
func loadModFrom(gomod string) (ret *gopmod.Module, err error) {
	if ret, err = doLoadModFrom(gomod); err == nil {
		ret.ImportClasses()
	}
	return
}

func doLoadModFrom(gomod string) (ret *gopmod.Module, err error) {
	dir, _ := filepath.Split(gomod)
	return gopmod.LoadFrom(gomod, dir+"gop.mod")
}

// LoadMod loads a Go+ module.
func (p *Context) LoadMod(mod *Module) *gopmod.Module {
	if mod != nil {
		if r := mod.Replace; r != nil {
			mod = r
		}
		if mod.GoMod != "" {
			if ret, err := p.LoadModFrom(mod.GoMod); err == nil {
				return ret
			}
		}
	}
	return gopmod.Default
}
