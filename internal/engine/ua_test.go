package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 回归：真实镜像站（TUNA/BFSU/Aliyun/Huawei/USTC）会拒绝 Go 默认 UA（403/418/204/429）。
// 探测与测速请求必须带 UserAgent，否则所有国内下行候选源都会“探测失败”。
func TestProbeSendsCustomUserAgent(t *testing.T) {
	body := strings.Repeat("x", 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != UserAgent {
			http.Error(w, "blocked", 403)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	pr := Probe(context.Background(), srv.Client(), Endpoint{ID: "t", Label: "t", Dir: "down", URL: srv.URL})
	if !pr.OK || pr.Bytes < 200000 {
		t.Fatalf("probe failed: %+v", pr)
	}
}
