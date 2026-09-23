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

func TestDeviceMappings(t *testing.T) {
	got := deviceMappings([]string{"/dev/dri", "/dev/foo:/dev/bar"})
	if len(got) != 2 {
		t.Fatalf("mappings=%v", got)
	}
	if got[0]["PathOnHost"] != "/dev/dri" || got[0]["PathInContainer"] != "/dev/dri" {
		t.Fatalf("bare path mapping=%v", got[0])
	}
	if got[0]["CgroupPermissions"] != "rwm" {
		t.Fatalf("permissions=%v", got[0])
	}
	if got[1]["PathOnHost"] != "/dev/foo" || got[1]["PathInContainer"] != "/dev/bar" {
		t.Fatalf("explicit mapping=%v", got[1])
	}
	if len(deviceMappings(nil)) != 0 {
		t.Fatal("nil devices must map to none")
	}
}
