package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var files embed.FS

func Handler() http.Handler { f, _ := fs.Sub(files, "assets"); return http.FileServer(http.FS(f)) }
