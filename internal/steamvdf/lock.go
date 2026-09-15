package steamvdf

import (
	"fmt"
	"github.com/gofrs/flock"
)

// Lock serialises read-modify-write transactions by cooperating CLI processes.
// It cannot lock Steam's in-memory configuration; callers must check Steam too.
func Lock(path string) (func(), error) {
	f := flock.New(path + ".steamcli-lock")
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("configuration is being edited by another process: %s", path)
	}
	return func() { _ = f.Unlock() }, nil
}
