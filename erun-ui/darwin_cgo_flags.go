//go:build darwin

package main

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo CXXFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework UniformTypeIdentifiers -mmacosx-version-min=11.0
*/
import "C"
