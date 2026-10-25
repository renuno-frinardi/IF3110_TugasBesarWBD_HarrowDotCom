package web

import (
	"embed"
	"html/template"
	"io/fs"
	"strings"
	"time"
	"unicode/utf8"
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
		"formatTimePtr": func(value *time.Time) string {
			if value == nil || value.IsZero() {
				return "-"
			}
			return value.Local().Format("02 Jan 2006, 15:04:05")
		},
		"add":      func(value, amount int) int { return value + amount },
		"subtract": func(value, amount int) int { return value - amount },
		"initial": func(value string) string {
			value = strings.TrimSpace(value)
			if value == "" {
				return "?"
			}

			runeValue, _ := utf8.DecodeRuneInString(value)
			return strings.ToUpper(string(runeValue))
		},
		"csrfField": func(token string) template.HTML {
			return template.HTML(`<input type="hidden" name="csrf_token" value="` + template.HTMLEscapeString(token) + `" />`)
		},
		"derefInt": func(value *int) int {
			if value == nil {
				return 0
			}
			return *value
		},
		"derefString": func(value *string) string {
			if value == nil {
				return ""
			}
			return *value
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
