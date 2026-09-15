package meta

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every module that ships inside the binary must appear in the provenance
// document, because its notice has to travel with any redistribution. Four
// modules had been vendored without being recorded; this keeps that from
// recurring silently.
func TestEveryVendoredModuleIsDocumented(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../docs/THIRD_PARTY.md")
	if err != nil {
		t.Fatal(err)
	}

	// Module lines inside a require block: "\tpath vX.Y.Z" with optional comment.
	line := regexp.MustCompile(`(?m)^\s+((?:github\.com|golang\.org|gopkg\.in)/[^\s]+)\s+v[^\s]+`)
	matches := line.FindAllStringSubmatch(string(mod), -1)
	if len(matches) == 0 {
		t.Fatal("no modules found in go.mod; the parser needs updating")
	}

	for _, m := range matches {
		path := m[1]
		if !strings.Contains(string(doc), path) {
			t.Errorf("%s is vendored but absent from docs/THIRD_PARTY.md", path)
		}
	}
}
