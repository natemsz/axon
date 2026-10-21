//go:build !windows && !darwin && !linux

package install

import "errors"

// Summary says, after installing, where Axon now is.
const Summary = "EveSynapse Axon is installed."

var errUnsupported = errors.New("installing is not supported on this system: run the program where it is")

func platform(Layout, string, func(string, ...any)) error { return errUnsupported }

func unplatform(Layout, func(string, ...any)) error { return errUnsupported }
