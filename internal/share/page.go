package share

import (
	"crypto/rand"
	"encoding/base64"
	"html"
	"net/http"
	"strings"
)

func writeGrabPage(w http.ResponseWriter, id string) {
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		http.Error(w, "weazlcloud: unavailable", http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	csp := w.Header().Get("Content-Security-Policy")
	csp = strings.Replace(csp, "script-src 'self'", "script-src 'self' 'nonce-"+nonce+"'", 1)
	csp = strings.Replace(csp, "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1)
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="theme-color" content="#111215"><title>Grab / WeazlCloud</title>
<style nonce="` + nonce + `">
:root{color-scheme:dark;--bg:#111215;--text:#e9e8e1;--muted:#9798a5;--yellow:#e6e68a;--purple:#a17bff;--line:#2b2d33}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:radial-gradient(ellipse at top,#26212f,transparent 65%);color:var(--text);font:14px Arial,Helvetica,sans-serif}
.card{width:min(420px,100%);padding:32px;border:1px solid #494052;background:#18191e;border-top:3px solid var(--purple)}
h1{color:var(--yellow);font-size:clamp(28px,8vw,42px);letter-spacing:-2px;line-height:1.05;margin:16px 0 10px;word-break:break-word}
.meta,.eyebrow{color:var(--muted);font-size:12px;line-height:1.6}
.eyebrow{font:9px monospace;letter-spacing:1.4px;color:var(--purple)}
label{display:grid;gap:8px;font-size:11px;margin:18px 0}
input{background:#141518;border:1px solid #49464f;color:var(--text);padding:12px;font:inherit}
button{width:100%;border:0;background:var(--yellow);color:#151515;padding:12px;font:inherit;cursor:pointer;margin-top:8px}
.list{border-top:1px solid var(--line);margin:18px 0}
.row{padding:12px 0;border-bottom:1px solid #24262c;font-size:13px}
.err{color:#f3a3ad;min-height:1.4em;font-size:12px}
</style></head><body>
<main class="card">
<p class="eyebrow">WEAZLCLOUD / GRAB</p>
<h1 id="t">Loading…</h1>
<p class="meta" id="m"></p>
<form id="f" hidden><label>Passphrase<input name="phrase" type="password" autocomplete="off"></label></form>
<div class="list" id="l" hidden></div>
<button type="button" id="g" hidden>Grab →</button>
<p class="err" id="e"></p>
<p class="eyebrow">NO ACCOUNT. ONE LINK. THEN GONE.</p>
</main>
<script nonce="` + nonce + `">
const id = ` + "`" + html.EscapeString(id) + "`" + `;
const $ = s => document.querySelector(s);
async function meta(){
  const r = await fetch('/g/'+id+'/meta');
  const j = await r.json();
  if (!r.ok || j.status === 'burned') { gone(j.error); return; }
  $('#t').textContent = j.name;
  $('#m').textContent = [j.size+' B', j.gate, 'read-only'].join(' · ');
  if (j.gate === 'passphrase') $('#f').hidden = false;
  if (j.kind === 'folder' && j.files) {
    $('#l').hidden = false;
    $('#l').innerHTML = j.files.map(f => '<div class="row"><strong>'+f.title+'</strong></div>').join('');
    $('#g').textContent = 'Grab folder →';
  }
  $('#g').hidden = false;
  $('#g').onclick = grab;
}
function gone(msg){ $('#t').textContent = 'This grab is gone.'; $('#m').textContent = msg || 'Burned after read.'; $('#f').hidden = true; $('#g').hidden = true; $('#l').hidden = true; }
async function grab(){
  $('#e').textContent = '';
  const phrase = ($('#f [name=phrase]')||{}).value || '';
  const r = await fetch('/g/'+id+'/file', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({passphrase:phrase})});
  if (!r.ok) { const j = await r.json().catch(()=>({})); if (r.status===401){ $('#e').textContent='Wrong passphrase. Phrase rides a second channel.'; return;} gone(j.error); return; }
  const blob = await r.blob();
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = ($('#t').textContent||'grab');
  a.click();
  $('#t').textContent = 'Grabbed.';
  $('#m').textContent = 'This link is done. There is no account to come back to.';
  $('#f').hidden = true; $('#g').hidden = true; $('#l').hidden = true;
}
meta().catch(() => gone());
</script>
</body></html>`))
}
