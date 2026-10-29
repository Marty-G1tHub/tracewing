package report

import (
	"strings"
	"testing"
)

func TestBuildSelfContained(t *testing.T) {
	b, err := Build("vps", map[string]any{"x": "</script><b>"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, ph := range []string{"/*CSS*/", "/*JS*/", "/*DATA*/", "/*KIND*/"} {
		if strings.Contains(s, ph) {
			t.Fatal("placeholder left:", ph)
		}
	}
	if strings.Contains(s, "</script><b>") {
		t.Fatal("data must be escaped")
	}
	for _, bad := range []string{"http://fonts.", "https://fonts.", "cdn."} {
		if strings.Contains(s, bad) {
			t.Fatal("external asset:", bad)
		}
	}
}
