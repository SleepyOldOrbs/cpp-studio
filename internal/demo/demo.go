package demo

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFiles embed.FS

// Handler returns an embeddable static browser demo for the cpp-studio gateway.
func Handler() http.Handler {
	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("demo static files: " + err.Error())
	}
	return http.FileServer(http.FS(content))
}
