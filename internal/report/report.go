// Package report 生成离线单文件 HTML 报告：CSS、渲染 JS 与数据全部内联，U 盘/邮件里直接打开，浏览器打印即得 PDF。
package report

import (
	"encoding/json"
	"strings"

	"github.com/Marty-G1tHub/tracewing/internal/web"
)

// Build 渲染逻辑与在线页面共用 web/render.js，Go 侧只负责把数据塞进模板。
func Build(kind string, summary any) ([]byte, error) {
	data, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	// 防止数据中的 </script> 提前结束脚本块
	safe := strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(data))
	k, _ := json.Marshal(kind)
	out := web.Read("report.html")
	out = strings.NewReplacer(
		"/*CSS*/", web.Read("app.css"),
		"/*KIND*/", string(k),
	).Replace(out)
	// JS 与 DATA 分步替换，避免数据里恰好含占位符被二次替换
	out = strings.Replace(out, "/*JS*/", web.Read("render.js"), 1)
	out = strings.Replace(out, "/*DATA*/", safe, 1)
	return []byte(out), nil
}
