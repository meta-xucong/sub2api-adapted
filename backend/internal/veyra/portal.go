package veyra

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:portal_dist
var portalFS embed.FS

type PortalConfig struct {
	Enabled       bool
	PortalEnabled bool
}

func (c PortalConfig) IsPortalEnabled() bool {
	return c.Enabled && c.PortalEnabled
}

func PortalMiddleware(cfg PortalConfig) gin.HandlerFunc {
	if !cfg.IsPortalEnabled() {
		return func(c *gin.Context) {
			c.Next()
		}
	}
	subFS, err := fs.Sub(portalFS, "portal_dist")
	if err != nil {
		panic("veyra portal dist missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(subFS))

	return func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		switch {
		case requestPath == "/":
			servePortalFile(c, subFS, "index.html")
			return
		case requestPath == "/_veyra/return":
			servePortalFile(c, subFS, "index.html")
			return
		case requestPath == "/_veyra":
			c.Redirect(http.StatusMovedPermanently, "/_veyra/")
			c.Abort()
			return
		case strings.HasPrefix(requestPath, "/_veyra/"):
			cleanPath := strings.TrimPrefix(path.Clean(requestPath), "/_veyra/")
			if cleanPath == "." || cleanPath == "" {
				cleanPath = "mobile.html"
			}
			if !portalFileExists(subFS, cleanPath) {
				c.String(http.StatusNotFound, "Veyra asset not found")
				c.Abort()
				return
			}
			c.Request.URL.Path = "/" + cleanPath
			fileServer.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		default:
			c.Next()
		}
	}
}

func servePortalFile(c *gin.Context, fsys fs.FS, name string) {
	content, err := fs.ReadFile(fsys, name)
	if err != nil {
		c.String(http.StatusInternalServerError, "Veyra portal unavailable")
		c.Abort()
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, contentType(name), content)
	c.Abort()
}

func portalFileExists(fsys fs.FS, name string) bool {
	file, err := fsys.Open(name)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func contentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
