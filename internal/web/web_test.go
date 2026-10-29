package web

import (
	"strings"
	"testing"
)

// 用户可见文案统一写 “VPS测速”，不允许小写 vps 开头的中文标签。
func TestVPSLabelUppercase(t *testing.T) {
	for _, f := range []string{"index.html", "render.js", "app.js", "report.html"} {
		s := Read(f)
		for _, bad := range []string{"vps测速", "vps 测速"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s 含小写标签 %q", f, bad)
			}
		}
	}
	if !strings.Contains(Read("render.js"), "VPS测速") || !strings.Contains(Read("index.html"), "VPS测速") {
		t.Error("缺少 VPS测速")
	}
}

// 历史详情里展开“去程”等折叠块时，宽表格不能溢出窄卡片压到相邻卡片上。
func TestFoldBodyScrollsInsteadOfOverflowing(t *testing.T) {
	if !strings.Contains(Read("render.js"), `<div class="fold-b">${body}</div></details>`) {
		t.Error("render.js 的 fold() 应把内容包在 .fold-b 里")
	}
	if !strings.Contains(Read("app.css"), ".fold-b{overflow-x:auto;min-width:0}") {
		t.Error("app.css 缺少 .fold-b 横向滚动规则")
	}
	if !strings.Contains(Read("app.js"), "backToList(true)") {
		t.Error("从历史详情返回时应还原列表滚动位置")
	}
}

// 窄窗口（≤860px）侧栏收成图标栏：网络药丸里的文字必须整体隐藏。
// 只靠父元素 font-size:0 不够——<b>/<em> 有自己的字号，会在 0 宽里一字一行竖着排。
func TestNarrowSidebarHidesNetpillText(t *testing.T) {
	css := Read("app.css")
	i := strings.Index(css, "@media (max-width:860px){")
	if i < 0 {
		t.Fatal("app.css 缺少 860px 断点")
	}
	block := css[i:]
	if j := strings.Index(block, "\n"); j > 0 {
		block = block[:j]
	}
	if !strings.Contains(block, ".netpill span,.netpill svg{display:none}") {
		t.Error("860px 断点里应隐藏 .netpill span，否则“网络环境”会竖排")
	}
}
