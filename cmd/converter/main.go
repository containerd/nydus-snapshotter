package main

// Import the converter package so that it can be compiled during
// `go build` to ensure cross-compilation compatibility.
import (
	_ "github.com/containerd/nydus-snapshotter/pkg/converter"
)

func main() {
}
