// Package web 内嵌前端静态文件（单页，无构建步骤，无外部依赖）。
package web

import "embed"

//go:embed index.html app.css render.js app.js report.html
var FS embed.FS

func Read(name string) string {
	b, _ := FS.ReadFile(name)
	return string(b)
}
