package orchestrator

import "testing"

func TestStripDockerStreamPlain(t *testing.T) {
	if got := stripDockerStream([]byte(`{"clock":true}`)); got != `{"clock":true}` {
		t.Fatalf("got %q", got)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(ErrNotFound) {
		t.Fatal("plain ErrNotFound")
	}
}
