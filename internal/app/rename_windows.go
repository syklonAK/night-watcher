package app

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// A Windows reader can briefly deny replacement while its file handle is open.
// Retry the same atomic rename; never truncate the live configuration.
func renameAtomic(from, to string) error {
	until := time.Now().Add(2 * time.Second)
	for {
		e := os.Rename(from, to)
		if e == nil {
			return nil
		}
		if !errors.Is(e, syscall.Errno(32)) && !errors.Is(e, syscall.ERROR_ACCESS_DENIED) {
			return e
		}
		if time.Now().After(until) {
			return e
		}
		time.Sleep(5 * time.Millisecond)
	}
}
