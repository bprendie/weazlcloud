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

func TestRenderedPreviewXLSXResolvesSharedCells(t *testing.T) {
	data := officeZip(t, map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>Alice</t></si><si><r><t>Shared </t></r><r><t>name</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1"><v>42</v></c></row></sheetData></worksheet>`,
	})
	body, contentType, ok := renderedPreview("book.xlsx", data)
	text := string(body)
	if !ok || contentType != "text/plain; charset=utf-8" || !strings.Contains(text, "A=Alice") || !strings.Contains(text, "B=Shared name") || !strings.Contains(text, "C=42") {
		t.Fatalf("unexpected XLSX preview: %v %s %q", ok, contentType, text)
	}
}

func TestRenderedPreviewPPTXSortsSlideNumbers(t *testing.T) {
	data := officeZip(t, map[string]string{
		"ppt/slides/slide10.xml": `<s><t>Ten</t></s>`,
		"ppt/slides/slide2.xml":  `<s><t>Two</t></s>`,
	})
	body, _, ok := renderedPreview("deck.pptx", data)
	text := string(body)
	if !ok || strings.Index(text, "Slide 2: Two") > strings.Index(text, "Slide 10: Ten") {
		t.Fatalf("slides were not sorted numerically: %q", text)
	}
}

func TestRenderedPreview3MFHonorsComponentTransform(t *testing.T) {
	data := officeZip(t, map[string]string{
		"3D/3dmodel.model": `<model><resources><object id="1"><mesh><vertices><vertex x="0" y="0" z="0"/><vertex x="1" y="0" z="0"/><vertex x="0" y="1" z="0"/></vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object><object id="2"><components><component objectid="1" transform="1 0 0 0 1 0 0 0 1 10 0 0"/></components></object></resources><build><item objectid="2"/></build></model>`,
	})
	mesh, ok := parse3MF(data)
	if !ok || len(mesh.triangles) != 1 || len(mesh.vertices) != 3 {
		t.Fatalf("unexpected 3MF mesh: %v vertices=%d triangles=%d", ok, len(mesh.vertices), len(mesh.triangles))
	}
	if mesh.vertices[0][0] != 10 {
		t.Fatalf("component transform was not applied: %#v", mesh.vertices)
	}
}

func officeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
