// Package web embeds the OpenForge frontend assets into the binary.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html static
var content embed.FS

// Templates parses the server-side HTML templates.
func Templates() (*template.Template, error) {
	return template.ParseFS(content, "templates/*.html")
}

// Static returns a handler for the embedded static assets.
func Static() http.Handler {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

// ReadFile returns the contents of an embedded static file.
func ReadFile(name string) ([]byte, error) {
	return content.ReadFile("static/" + name)
}
