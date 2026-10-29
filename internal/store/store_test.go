package store

import (
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := NewID("vps", time.Now())
	e := Entry{ID: id, Kind: "vps", At: "2026-10-27T10:00:00+08:00", Server: "h:1", Source: "server", Period: "off_peak", Med: map[string]float64{"fwd1": 50}}
	if err := s.Save(e, map[string]int{"a": 1}, []byte("<html>")); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 1 || l[0].ID != id {
		t.Fatal(l)
	}
	if h := s.VPSHistory("h:1", "server"); len(h) != 1 || h[0].Med["fwd1"] != 50 {
		t.Fatal(h)
	}
	if h := s.VPSHistory("other", "server"); len(h) != 0 {
		t.Fatal(h)
	}
	if b, err := s.Summary(id); err != nil || len(b) == 0 {
		t.Fatal(err)
	}
	if _, err := s.Summary("../../etc/passwd"); err == nil {
		t.Fatal("path traversal")
	}
	if err := s.Delete(id); err != nil || len(s.List()) != 0 {
		t.Fatal(err)
	}
}
