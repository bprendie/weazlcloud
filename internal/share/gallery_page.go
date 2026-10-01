package share

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
)

func writeGalleryPage(w http.ResponseWriter, id string) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "unavailable", 500)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	csp := w.Header().Get("Content-Security-Policy")
	csp = strings.Replace(csp, "script-src 'self'", "script-src 'self' 'nonce-"+nonce+"'", 1)
	csp = strings.Replace(csp, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Photos / WeazlCloud</title><style nonce="` + nonce + `">
:root{color-scheme:dark}*{box-sizing:border-box}body{margin:0;background:#111215;color:#e9e8e1;font:16px system-ui}main{max-width:1400px;margin:auto;padding:24px}header{display:flex;align-items:center;gap:16px;flex-wrap:wrap}h1{flex:1;overflow-wrap:anywhere}button,input{font:inherit;padding:10px;border:1px solid #a17bff;background:#24202d;color:inherit;border-radius:6px;cursor:pointer}p{color:#a4a3b0}#grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(170px,100%),1fr));gap:12px}.tile{position:relative;min-width:0}.tile button{width:100%;padding:0;aspect-ratio:1;overflow:hidden}.tile img{width:100%;height:100%;object-fit:cover}.tile label{position:absolute;top:8px;left:8px}.tile p{font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}dialog{width:100vw;height:100dvh;max-width:none;max-height:none;border:0;background:#111215;color:inherit;padding:20px}dialog[open]{display:flex;flex-direction:column}dialog header{flex:none}dialog img{width:100%;min-height:0;flex:1;object-fit:contain}#error{color:#f3a3ad}[hidden]{display:none!important}iframe{display:none}
</style></head><body><main><header><h1 id="title">Photos</h1><button id="all" hidden>Download album</button><button id="selected" hidden>Download selected</button></header><form id="gate" hidden><label>Passphrase <input name="passphrase" type="password" required autocomplete="off"></label><button>Open gallery</button></form><p id="note">Loading…</p><p id="error" role="alert"></p><div id="grid"></div><nav aria-label="Gallery pages"><button id="page-prev" hidden>Previous page</button> <span id="page-number"></span> <button id="more" hidden>Next page</button></nav><p>Previews omit embedded location metadata. Original downloads may contain it.</p></main><dialog id="viewer"><header><button id="close">Close</button><button id="prev">Previous</button><strong id="name"></strong><button id="next">Next</button><button id="original">Download original</button></header><img id="large" alt=""></dialog><iframe name="download-target" title="Download"></iframe><script nonce="` + nonce + `">
const base='/g/` + id + `/', ` + galleryScript + `
</script></body></html>`))
}
