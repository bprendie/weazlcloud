package desk

import (
	"net/http"
	"net/url"

	qrcode "github.com/skip2/go-qrcode"
)

func (h *Handler) qr(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	u, err := url.ParseRequestURI(raw)
	if err != nil || raw == "" || u.Scheme != "https" || u.Host == "" {
		http.Error(w, `{"error":"invalid QR URL"}`, http.StatusBadRequest)
		return
	}
	png, err := qrcode.Encode(raw, qrcode.Medium, 320)
	if err != nil {
		http.Error(w, `{"error":"QR generation failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
