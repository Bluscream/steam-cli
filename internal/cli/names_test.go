package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Cobra resolves an argument against names and aliases in registration order
// and takes the first match, so a duplicate silently shadows whichever command
// was added second. Two have slipped in this way already: "players" as an alias
// of "web player" hid the player-count command, and "compat" on "library custom"
// hid "library compat". Neither produced an error, only the wrong command.
func TestNoDuplicateNamesAmongSiblings(t *testing.T) {
	root := New(strings.NewReader(""), io.Discard, io.Discard)
	var walk func(*cobra.Command, string)
	walk = func(c *cobra.Command, path string) {
		seen := map[string]string{}
		for _, sub := range c.Commands() {
			for _, name := range append([]string{sub.Name()}, sub.Aliases...) {
				if prev, dup := seen[name]; dup {
					t.Errorf("%s: %q is used by both %q and %q; the second is unreachable",
						path, name, prev, sub.Name())
					continue
				}
				seen[name] = sub.Name()
			}
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	walk(root, "steamcli")
}
