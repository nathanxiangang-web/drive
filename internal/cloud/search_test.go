package cloud

import (
	"errors"
	"testing"
)

func TestSearchParentRequiresScopedAncestry(t *testing.T) {
	chain := []breadcrumb{{"0", "根目录"}, {"1", "Shared"}, {"2", "影视"}, {"3", "Bilby"}}
	if got, err := relativeParent(chain, "1", "3"); err != nil || got != "影视/Bilby" {
		t.Fatalf("nested result: %q %v", got, err)
	}
	if _, err := relativeParent(chain, "5", "3"); !errors.Is(err, Missing) {
		t.Fatal("outside parent was accepted")
	}
	if _, err := relativeParent(chain, "1", "2"); !errors.Is(err, Missing) {
		t.Fatal("wrong requested parent was accepted")
	}
}
