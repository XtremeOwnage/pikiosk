package web

import (
	"bytes"
	_ "embed"
	"net/http"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// api.md is also the REST API page of the documentation site.
//
//go:embed api.md
var apiMarkdown []byte

var apiPage = sync.OnceValue(func() []byte {
	var body bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := md.Convert(apiMarkdown, &body); err != nil {
		return []byte("API docs failed to render: " + err.Error())
	}
	var out bytes.Buffer
	out.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pikiosk REST API</title>
<link rel="stylesheet" href="style.css">
</head><body><main class="doc">
<p><a href="./">← Back to the kiosk</a></p>
`)
	out.Write(body.Bytes())
	out.WriteString("</main></body></html>\n")
	return out.Bytes()
})

func serveAPIDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(apiPage())
}
