//go:build !windows

package app

import "os"

func renameAtomic(from, to string) error { return os.Rename(from, to) }
