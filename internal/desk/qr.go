package desk

import (
	"image/color"
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
	code, err := qrcode.New(raw, qrcode.Medium)
	if err != nil {
		http.Error(w, `{"error":"QR generation failed"}`, http.StatusInternalServerError)
		return
	}
	code.ForegroundColor = color.RGBA{R: 212, G: 92, B: 255, A: 255}
	code.BackgroundColor = color.RGBA{R: 16, G: 17, B: 20, A: 255}
	png, err := code.PNG(320)
	if err != nil {
		http.Error(w, `{"error":"QR generation failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
