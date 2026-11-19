package download

import "testing"

func TestSourceKeepsIdentityAndRejectsSelectiveDownloads(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	base := "magnet:?xt=urn:btih:" + hash
	got, id, err := Prepare(base + "&amp;dn=sample&amp;tr=udp%3A%2F%2Ftracker.invalid")
	if err != nil || id != hash || got != base+"&dn=sample" {
		t.Fatalf("normalized input: %q %q %v", got, id, err)
	}
	for _, s := range []string{base + "&amp;%73o=0", base + "&xt=urn:btih:1111111111111111111111111111111111111111", "magnet:?dn=no-hash", "https://127.0.0.1/file"} {
		if _, _, err := Prepare(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
