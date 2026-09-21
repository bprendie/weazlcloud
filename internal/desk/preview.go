package desk

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type previewMesh struct {
	vertices  [][3]float64
	triangles [][3]int
}

func renderedPreview(path string, data []byte) ([]byte, string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".stl":
		if mesh, ok := parseSTL(data); ok {
			return meshSVG(mesh), "image/svg+xml", true
		}
	case ".3mf":
		if mesh, ok := parse3MF(data); ok {
			return meshSVG(mesh), "image/svg+xml", true
		}
	case ".docx":
		return zipText(data, []string{"word/document.xml"}, "DOCX"), "text/plain; charset=utf-8", true
	case ".xlsx":
		return xlsxText(data), "text/plain; charset=utf-8", true
	case ".pptx":
		return pptxText(data), "text/plain; charset=utf-8", true
	case ".odt", ".ods", ".odp":
		return zipText(data, []string{"content.xml"}, strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))), "text/plain; charset=utf-8", true
	}
	return nil, "", false
}

func zipText(data []byte, names []string, label string) []byte {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return []byte(label + " preview unavailable\n")
	}
	var files []*zip.File
	for _, f := range z.File {
		for _, name := range names {
			if f.Name == name || strings.HasSuffix(name, "/") && strings.HasPrefix(f.Name, name) {
				files = append(files, f)
				break
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var out strings.Builder
	out.WriteString(label + " preview\n\n")
	for _, f := range files {
		r, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(r, 8<<20))
		_ = r.Close()
		if err != nil {
			continue
		}
		text := xmlText(b)
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n")
		}
		if out.Len() > 12000 {
			break
		}
	}
	if out.Len() == len(label)+10 {
		out.WriteString("Preview unavailable\n")
	}
	return []byte(out.String())
}

func xmlText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out strings.Builder
	for {
		t, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		if ch, ok := t.(xml.CharData); ok {
			text := strings.TrimSpace(string(ch))
			if text != "" {
				if out.Len() > 0 {
					out.WriteByte(' ')
				}
				out.WriteString(text)
			}
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func parseSTL(data []byte) (previewMesh, bool) {
	var mesh previewMesh
	if len(data) >= 84 {
		n := int(binary.LittleEndian.Uint32(data[80:84]))
		if n > 0 && n <= 200000 && 84+n*50 <= len(data) {
			for i := 0; i < n && len(mesh.triangles) < 20000; i++ {
				base := 84 + i*50
				tri := [3]int{}
				points := [3][3]float64{}
				valid := true
				for j := 0; j < 3; j++ {
					off := base + 12 + j*12
					points[j] = [3]float64{float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off:]))), float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off+4:]))), float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off+8:])))}
					if !finitePoint(points[j]) {
						valid = false
					}
				}
				if !valid {
					continue
				}
				for j, point := range points {
					tri[j] = len(mesh.vertices)
					mesh.vertices = append(mesh.vertices, point)
				}
				mesh.triangles = append(mesh.triangles, tri)
			}
			return mesh, len(mesh.triangles) > 0
		}
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	var tri [3]int
	count := 0
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) != 4 || strings.ToLower(f[0]) != "vertex" {
			continue
		}
		x, e1 := strconv.ParseFloat(f[1], 64)
		y, e2 := strconv.ParseFloat(f[2], 64)
		z, e3 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil || e3 != nil || !finitePoint([3]float64{x, y, z}) {
			continue
		}
		tri[count%3] = len(mesh.vertices)
		mesh.vertices = append(mesh.vertices, [3]float64{x, y, z})
		count++
		if count%3 == 0 {
			mesh.triangles = append(mesh.triangles, tri)
			if len(mesh.triangles) == 20000 {
				break
			}
		}
	}
	return mesh, len(mesh.triangles) > 0
}

func finitePoint(point [3]float64) bool {
	return !math.IsNaN(point[0]) && !math.IsNaN(point[1]) && !math.IsNaN(point[2]) && !math.IsInf(point[0], 0) && !math.IsInf(point[1], 0) && !math.IsInf(point[2], 0)
}

func meshSVG(m previewMesh) []byte {
	if len(m.triangles) == 0 {
		return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 480"><rect width="100%" height="100%" fill="#101114"/><text x="24" y="40" fill="#a7a9b8" font-family="monospace">Preview unavailable</text></svg>`)
	}
	minX, maxX, minY, maxY, minZ, maxZ := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, v := range m.vertices {
		if !finitePoint(v) {
			return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 480"><rect width="100%" height="100%" fill="#101114"/><text x="24" y="40" fill="#a7a9b8" font-family="monospace">Preview unavailable</text></svg>`)
		}
		minX = math.Min(minX, v[0])
		maxX = math.Max(maxX, v[0])
		minY = math.Min(minY, v[1])
		maxY = math.Max(maxY, v[1])
		minZ = math.Min(minZ, v[2])
		maxZ = math.Max(maxZ, v[2])
	}
	_ = minZ
	_ = maxZ
	if maxX-minX == 0 {
		maxX = minX + 1
	}
	if maxY-minY == 0 {
		maxY = minY + 1
	}
	scale := math.Min(560/(maxX-minX), 400/(maxY-minY))
	project := func(v [3]float64) [2]float64 { return [2]float64{40 + (v[0]-minX)*scale, 440 - (v[1]-minY)*scale} }
	var out strings.Builder
	out.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 480"><rect width="100%" height="100%" fill="#101114"/><g fill="#29233a" fill-opacity=".72" stroke="#d45cff" stroke-opacity=".7" stroke-width=".7">`)
	for _, t := range m.triangles {
		a, b, c := project(m.vertices[t[0]]), project(m.vertices[t[1]]), project(m.vertices[t[2]])
		fmt.Fprintf(&out, `<path d="M%.1f %.1f L%.1f %.1f L%.1f %.1f Z"/>`, a[0], a[1], b[0], b[1], c[0], c[1])
	}
	out.WriteString(`</g><text x="18" y="466" fill="#a7a9b8" font-family="monospace" font-size="11">SERVER PREVIEW · `)
	out.WriteString(html.EscapeString(strconv.Itoa(len(m.triangles)) + " triangles"))
	out.WriteString(`</text></svg>`)
	return []byte(out.String())
}
