package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/store"
)

func start(t *testing.T) (*App, *http.Client, string, *[]string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	a := &App{Version: "t", OS: "test", Store: st, Quit: func() {}, Open: func(p string) error { opened = append(opened, p); return nil }}
	ln, entry, err := a.Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.Serve(ctx, ln, time.Hour)
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar}
	u, _ := url.Parse(entry)
	if r, err := cl.Get(entry); err != nil {
		t.Fatal(err)
	} else {
		r.Body.Close()
	}
	return a, cl, u.Scheme + "://" + u.Host, &opened
}

func post(t *testing.T, cl *http.Client, u, body string) *http.Response {
	t.Helper()
	r, err := cl.Post(u, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r
}

func TestInfoModeNotice(t *testing.T) {
	a, cl, base, _ := start(t)
	a.SetMode("browser", "没有 WebView2，已用浏览器打开")
	r, err := cl.Get(base + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var m map[string]string
	json.NewDecoder(r.Body).Decode(&m)
	if m["mode"] != "browser" || m["notice"] == "" || m["data_dir"] == "" {
		t.Fatal(m)
	}
}

func TestOpenReportValidatesID(t *testing.T) {
	a, cl, base, opened := start(t)
	for _, id := range []string{"../../etc/passwd", "", "x", `..\..\Windows\win.ini`} {
		if r := post(t, cl, base+"/api/open-report", `{"id":`+mustJSON(id)+`}`); r.StatusCode != 404 {
			t.Fatalf("id %q: %d", id, r.StatusCode)
		}
	}
	if len(*opened) != 0 {
		t.Fatal("must not open", *opened)
	}
	id := store.NewID("vps", time.Now())
	if err := a.Store.Save(store.Entry{ID: id, Kind: "vps", At: "2026-10-27T10:00:00+08:00"}, map[string]int{"a": 1}, []byte("<html>")); err != nil {
		t.Fatal(err)
	}
	if r := post(t, cl, base+"/api/open-report", `{"id":"`+id+`"}`); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if len(*opened) != 1 || filepath.Base(filepath.Dir((*opened)[0])) != id {
		t.Fatal(*opened)
	}
	os.Remove((*opened)[0])
	if r := post(t, cl, base+"/api/open-report", `{"id":"`+id+`"}`); r.StatusCode != 404 {
		t.Fatal("missing file", r.StatusCode)
	}
}

func TestNeedsAuthAndHost(t *testing.T) {
	_, _, base, _ := start(t)
	r, _ := http.Get(base + "/api/info")
	if r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	req, _ := http.NewRequest("GET", base+"/api/info", nil)
	req.Host = "evil.example:80"
	r, _ = http.DefaultClient.Do(req)
	if r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
