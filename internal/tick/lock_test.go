package tick

import (
	"path/filepath"
	"testing"
)

func TestLockIsExclusiveUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "tick.lock")

	release, ok, err := Lock(path)
	if err != nil || !ok {
		t.Fatalf("first Lock = %v, %v; want it to succeed", ok, err)
	}
	if _, ok, err := Lock(path); err != nil || ok {
		t.Fatalf("second Lock = %v, %v; want it to be refused while held", ok, err)
	}
	release()
	again, ok, err := Lock(path)
	if err != nil || !ok {
		t.Fatalf("Lock after release = %v, %v; want it to succeed", ok, err)
	}
	again()
}
