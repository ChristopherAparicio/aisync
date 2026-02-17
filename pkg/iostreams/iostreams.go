// Package iostreams provides I/O abstractions for the CLI.
// This allows commands to be tested without writing to real stdout/stderr.
package iostreams

import (
	"bytes"
	"io"
	"os"
)

// IOStreams holds the standard I/O streams for a command.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// System returns IOStreams connected to the real OS streams.
func System() *IOStreams {
	return &IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
}

// Test returns IOStreams backed by in-memory buffers for testing.
func Test() *IOStreams {
	return &IOStreams{
		In:     &bytes.Buffer{},
		Out:    &bytes.Buffer{},
		ErrOut: &bytes.Buffer{},
	}
}
