package httpapi

import (
	"embed"
	"net/http"
)

//go:embed web/fairpass.html
var frontendAssets embed.FS

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/fairpass.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	data, err := frontendAssets.ReadFile("web/fairpass.html")
	if err != nil {
		http.Error(w, "frontend unavailable", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}
