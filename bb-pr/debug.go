// Snapshot of hint-copy/debug.go as of 2029b96 (log path renamed).
// Do not import hint-copy — it is being refactored independently.
package main

import (
	"fmt"
	"os"
	"time"
)

// debugf appends a timestamped line to /tmp/bbpr-debug.log, but only when
// that file already exists (`touch` it to enable, remove to disable) — the
// viewer pane has no visible stderr, so this is the only way to observe
// startup behavior in a real herdr session.
func debugf(format string, args ...any) {
	if _, err := os.Stat("/tmp/bbpr-debug.log"); err != nil {
		return
	}
	f, err := os.OpenFile("/tmp/bbpr-debug.log", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%d] %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, args...))
}
