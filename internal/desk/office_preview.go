package desk

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const officePreviewLimit = 12000

type xlsxSharedItem struct {
	Text []string `xml:"t"`
	Runs []struct {
		Text []string `xml:"t"`
	} `xml:"r"`
}

type xlsxSharedStrings struct {
	Items []xlsxSharedItem `xml:"si"`
}

type xlsxCell struct {
	Ref    string   `xml:"r,attr"`
	Type   string   `xml:"t,attr"`
	Value  string   `xml:"v"`
	Inline []string `xml:"is>t"`
}

type xlsxRow struct {
	Number string     `xml:"r,attr"`
	Cells  []xlsxCell `xml:"c"`
}

type xlsxSheet struct {
	Rows []xlsxRow `xml:"sheetData>row"`
}

func xlsxText(data []byte) []byte {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return []byte("XLSX preview unavailable\n")
	}
	shared := readXLSXSharedStrings(z)
	files := matchingZipFiles(z.File, "xl/worksheets/")
	sort.SliceStable(files, func(i, j int) bool {
		return officeNumber(files[i].Name, "sheet") < officeNumber(files[j].Name, "sheet")
	})
	var out strings.Builder
	out.WriteString("XLSX preview (text only)\n\n")
	for _, f := range files {
		body, ok := readZipPreviewFile(f, 8<<20)
		if !ok {
			continue
		}
		var sheet xlsxSheet
		if xml.Unmarshal(body, &sheet) != nil {
			continue
		}
		fmt.Fprintf(&out, "Sheet %d\n", officeNumber(f.Name, "sheet"))
		for _, row := range sheet.Rows {
			if out.Len() >= officePreviewLimit {
				break
			}
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				value := xlsxCellText(cell, shared)
				if value == "" {
					continue
				}
				cells = append(cells, columnLabel(cell.Ref)+"="+value)
			}
			if len(cells) > 0 {
				label := row.Number
				if label == "" {
					label = "?"
				}
				fmt.Fprintf(&out, "row %s: %s\n", label, strings.Join(cells, " | "))
			}
		}
	}
	if out.Len() == len("XLSX preview (text only)\n\n") {
		out.WriteString("Preview unavailable\n")
	}
	return []byte(out.String())
}

func readXLSXSharedStrings(z *zip.Reader) []string {
	for _, f := range z.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		body, ok := readZipPreviewFile(f, 8<<20)
		if !ok {
			return nil
		}
		var source xlsxSharedStrings
		if xml.Unmarshal(body, &source) != nil {
			return nil
		}
		out := make([]string, len(source.Items))
		for i, item := range source.Items {
			parts := append([]string{}, item.Text...)
			for _, run := range item.Runs {
				parts = append(parts, run.Text...)
			}
			out[i] = strings.Join(parts, "")
		}
		return out
	}
	return nil
}

func xlsxCellText(cell xlsxCell, shared []string) string {
	switch cell.Type {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(cell.Value))
		if err == nil && i >= 0 && i < len(shared) {
			return shared[i]
		}
	case "inlineStr":
		return strings.Join(cell.Inline, "")
	case "b":
		if cell.Value == "1" {
			return "TRUE"
		}
		if cell.Value == "0" {
			return "FALSE"
		}
	}
	return strings.TrimSpace(cell.Value)
}

func columnLabel(ref string) string {
	ref = strings.TrimSpace(ref)
	end := 0
	for end < len(ref) && ref[end] >= 'A' && ref[end] <= 'Z' {
		end++
	}
	if end == 0 {
		return "cell"
	}
	return ref[:end]
}

func pptxText(data []byte) []byte {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return []byte("PPTX preview unavailable\n")
	}
	files := matchingZipFiles(z.File, "ppt/slides/")
	sort.SliceStable(files, func(i, j int) bool {
		return officeNumber(files[i].Name, "slide") < officeNumber(files[j].Name, "slide")
	})
	var out strings.Builder
	out.WriteString("PPTX preview (text only)\n\n")
	for _, f := range files {
		body, ok := readZipPreviewFile(f, 8<<20)
		if !ok {
			continue
		}
		text := xmlText(body)
		if text != "" {
			fmt.Fprintf(&out, "Slide %d: %s\n", officeNumber(f.Name, "slide"), text)
		}
		if out.Len() >= officePreviewLimit {
			break
		}
	}
	if out.Len() == len("PPTX preview (text only)\n\n") {
		out.WriteString("Preview unavailable\n")
	}
	return []byte(out.String())
}

func matchingZipFiles(files []*zip.File, prefix string) []*zip.File {
	out := make([]*zip.File, 0)
	for _, f := range files {
		if strings.HasPrefix(f.Name, prefix) && strings.HasSuffix(f.Name, ".xml") {
			out = append(out, f)
		}
	}
	return out
}

func readZipPreviewFile(f *zip.File, limit int64) ([]byte, bool) {
	r, err := f.Open()
	if err != nil {
		return nil, false
	}
	body, readErr := io.ReadAll(io.LimitReader(r, limit+1))
	_ = r.Close()
	if readErr != nil || int64(len(body)) > limit {
		return nil, false
	}
	return body, true
}

func officeNumber(name, prefix string) int {
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimPrefix(base, prefix)
	n, err := strconv.Atoi(base)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
}
