package cloud

import "testing"

func TestCheckedLinkUsesExactHTTPSHost(t *testing.T) {
	hosts := map[string]bool{"cdn.example.test": true}
	for _, tt := range []struct {
		link string
		ok   bool
	}{
		{"https://cdn.example.test/file?secret=opaque", true},
		{"https://cdn.example.test:443/file", true},
		{"http://cdn.example.test/file", false},
		{"https://cdn.example.test.evil/file", false},
		{"https://cdn.example.test@evil.test/file", false},
		{"https://cdn.example.test:444/file", false},
		{"https://cdn.example.test/file#", false},
		{"http://openlist:5244/p/file", false},
	} {
		if _, err := checkedLink(tt.link, hosts); (err == nil) != tt.ok {
			t.Errorf("unexpected outcome for %s", tt.link)
		}
	}
}
