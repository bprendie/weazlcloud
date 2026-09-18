package desk

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func TestRenderedPreviewSTL(t *testing.T) {
	data := make([]byte, 84+50)
	binary.LittleEndian.PutUint32(data[80:84], 1)
	for i, v := range [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}} {
		off := 96 + i*12
		binary.LittleEndian.PutUint32(data[off:], math.Float32bits(v[0]))
		binary.LittleEndian.PutUint32(data[off+4:], math.Float32bits(v[1]))
		binary.LittleEndian.PutUint32(data[off+8:], math.Float32bits(v[2]))
	}
	body, contentType, ok := renderedPreview("model.stl", data)
	if !ok || contentType != "image/svg+xml" || !bytes.Contains(body, []byte("1 triangles")) {
		t.Fatalf("unexpected STL preview: %v %s", ok, contentType)
	}
}

func TestRenderedPreviewOfficeText(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte(`<document><t>hello from docx</t></document>`))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	body, contentType, ok := renderedPreview("notes.docx", buf.Bytes())
	if !ok || contentType != "text/plain; charset=utf-8" || !strings.Contains(string(body), "hello from docx") {
		t.Fatalf("unexpected office preview: %v %s %q", ok, contentType, body)
	}
}
