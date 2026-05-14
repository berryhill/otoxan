package sessionflow

import (
	"log"
	"os"
	"path/filepath"
)

// LoadFromDir scans <home>/flows/*.yaml and registers any valid flow definitions
// found there.  This is the reserved hook for external flow loading; the
// implementation is deferred to a future plan.
//
// For v1 the function exists so callers (e.g. dispatch bootstrap) can invoke it
// unconditionally.  It currently only logs that the hook is reserved and returns
// without reading any files.
func LoadFromDir(home string) {
	flowsDir := filepath.Join(home, "flows")
	if _, err := os.Stat(flowsDir); os.IsNotExist(err) {
		log.Printf("sessionflow: flows dir %q does not exist, skipping external load", flowsDir)
		return
	}
	log.Printf("sessionflow: flows dir %q exists but external flow loading is not yet implemented (reserved hook)", flowsDir)
}
