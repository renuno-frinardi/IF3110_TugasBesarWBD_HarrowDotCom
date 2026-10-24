package web

import (
	"embed"
	"html/template"
	"io/fs"
	"time"
)

//go:embed templates static
var Files embed.FS

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatTime": func(value time.Time) string {
			if value.IsZero() {
				return "-"
			}
			return value.Local().Format("02 Jan 2006, 15:04")
		},
	}
}

func ParsePage(page string) (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(
		Files,
		"templates/base.html",
		"templates/"+page+".html",
	)
}

func StaticFS() fs.FS {
	static, err := fs.Sub(Files, "static")
	if err != nil {
		panic("Static directory is missing")
	}
	return static
}