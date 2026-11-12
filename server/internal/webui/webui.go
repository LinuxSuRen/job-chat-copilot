// Package webui 内嵌控制台静态资源。
//
// 构建产物约定：Makefile 在打包前将 web/dist 拷贝到本包 dist/ 目录。
// 仓库内始终保留占位 index.html，保证没有前端构建产物时也能编译（降级页）。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// FS 返回静态资源根（dist 目录内容）；dist 不存在真实产物时仅含降级占位页。
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return distFS
	}
	return sub
}

// Handler 返回静态文件服务（SPA 兜底：未知路径回退 index.html）。
func Handler() http.Handler {
	fileSys := http.FS(FS())
	fileServer := http.FileServer(fileSys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(FS(), p[1:]); err != nil {
			// SPA 路由兜底
			r2 := new(http.Request)
			*r2 = *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
