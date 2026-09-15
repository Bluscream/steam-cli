package idle

import (
	"context"
	"testing"
)

func TestEngineWithoutHelper(t *testing.T) {
	e := &Engine{
		DataDir:   t.TempDir(),
		HasHelper: false,
	}

	_, err := e.Start(context.Background(), []int{480}, "", "")
	if err == nil {
		t.Fatal("expected error when starting SDK idle without helper and without ASF")
	}

	msg, err := e.Stop(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if msg == "" {
		t.Fatal("expected stop message")
	}
}
