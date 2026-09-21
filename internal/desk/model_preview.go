package desk

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const max3MFModelBytes = 64 << 20

type model3MF struct {
	Resources model3MFResources `xml:"resources"`
	Build     model3MFBuild     `xml:"build"`
}

type model3MFResources struct {
	Objects []model3MFObject `xml:"object"`
}

type model3MFObject struct {
	ID         int                 `xml:"id,attr"`
	Mesh       *model3MFMesh       `xml:"mesh"`
	Components []model3MFComponent `xml:"components>component"`
}

type model3MFMesh struct {
	Vertices  []model3MFVertex   `xml:"vertices>vertex"`
	Triangles []model3MFTriangle `xml:"triangles>triangle"`
}

type model3MFVertex struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
	Z float64 `xml:"z,attr"`
}

type model3MFTriangle struct {
	A int `xml:"v1,attr"`
	B int `xml:"v2,attr"`
	C int `xml:"v3,attr"`
}

type model3MFComponent struct {
	ObjectID  int    `xml:"objectid,attr"`
	Transform string `xml:"transform,attr"`
}

type model3MFBuild struct {
	Items []model3MFItem `xml:"item"`
}

type model3MFItem struct {
	ObjectID  int    `xml:"objectid,attr"`
	Transform string `xml:"transform,attr"`
}

type modelTransform [12]float64

func identityTransform() modelTransform {
	return modelTransform{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}
}

func parseTransform(raw string) (modelTransform, bool) {
	if strings.TrimSpace(raw) == "" {
		return identityTransform(), true
	}
	parts := strings.Fields(raw)
	if len(parts) != 12 {
		return modelTransform{}, false
	}
	var out modelTransform
	for i, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return modelTransform{}, false
		}
		out[i] = value
	}
	return out, true
}

func multiplyTransform(a, b modelTransform) modelTransform {
	return modelTransform{
		a[0]*b[0] + a[1]*b[3] + a[2]*b[6],
		a[0]*b[1] + a[1]*b[4] + a[2]*b[7],
		a[0]*b[2] + a[1]*b[5] + a[2]*b[8],
		a[3]*b[0] + a[4]*b[3] + a[5]*b[6],
		a[3]*b[1] + a[4]*b[4] + a[5]*b[7],
		a[3]*b[2] + a[4]*b[5] + a[5]*b[8],
		a[6]*b[0] + a[7]*b[3] + a[8]*b[6],
		a[6]*b[1] + a[7]*b[4] + a[8]*b[7],
		a[6]*b[2] + a[7]*b[5] + a[8]*b[8],
		a[0]*b[9] + a[1]*b[10] + a[2]*b[11] + a[9],
		a[3]*b[9] + a[4]*b[10] + a[5]*b[11] + a[10],
		a[6]*b[9] + a[7]*b[10] + a[8]*b[11] + a[11],
	}
}

func applyTransform(t modelTransform, v model3MFVertex) [3]float64 {
	return [3]float64{
		t[0]*v.X + t[1]*v.Y + t[2]*v.Z + t[9],
		t[3]*v.X + t[4]*v.Y + t[5]*v.Z + t[10],
		t[6]*v.X + t[7]*v.Y + t[8]*v.Z + t[11],
	}
}

func parse3MF(data []byte) (previewMesh, bool) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return previewMesh{}, false
	}
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".model") {
			continue
		}
		r, openErr := f.Open()
		if openErr != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(r, max3MFModelBytes+1))
		_ = r.Close()
		if readErr != nil || len(body) > max3MFModelBytes {
			continue
		}
		var model model3MF
		if xml.Unmarshal(body, &model) != nil {
			continue
		}
		return assemble3MF(model), true
	}
	return previewMesh{}, false
}

func assemble3MF(model model3MF) previewMesh {
	objects := make(map[int]model3MFObject, len(model.Resources.Objects))
	ids := make([]int, 0, len(model.Resources.Objects))
	for _, object := range model.Resources.Objects {
		objects[object.ID] = object
		ids = append(ids, object.ID)
	}
	sort.Ints(ids)
	var mesh previewMesh
	if len(model.Build.Items) > 0 {
		for _, item := range model.Build.Items {
			transform, ok := parseTransform(item.Transform)
			if ok {
				append3MFObject(&mesh, objects, item.ObjectID, transform, map[int]bool{}, 0)
			}
			if len(mesh.triangles) >= 20000 {
				break
			}
		}
	} else {
		for _, id := range ids {
			append3MFObject(&mesh, objects, id, identityTransform(), map[int]bool{}, 0)
			if len(mesh.triangles) >= 20000 {
				break
			}
		}
	}
	return mesh
}

func append3MFObject(out *previewMesh, objects map[int]model3MFObject, id int, parent modelTransform, stack map[int]bool, depth int) {
	if depth > 32 || stack[id] || len(out.triangles) >= 20000 {
		return
	}
	object, ok := objects[id]
	if !ok {
		return
	}
	stack[id] = true
	defer delete(stack, id)
	if object.Mesh != nil && valid3MFMesh(*object.Mesh) {
		base := len(out.vertices)
		for _, vertex := range object.Mesh.Vertices {
			out.vertices = append(out.vertices, applyTransform(parent, vertex))
		}
		for _, triangle := range object.Mesh.Triangles {
			if triangle.A >= 0 && triangle.B >= 0 && triangle.C >= 0 && triangle.A < len(object.Mesh.Vertices) && triangle.B < len(object.Mesh.Vertices) && triangle.C < len(object.Mesh.Vertices) {
				out.triangles = append(out.triangles, [3]int{base + triangle.A, base + triangle.B, base + triangle.C})
				if len(out.triangles) >= 20000 {
					return
				}
			}
		}
	}
	for _, component := range object.Components {
		transform, ok := parseTransform(component.Transform)
		if ok {
			append3MFObject(out, objects, component.ObjectID, multiplyTransform(parent, transform), stack, depth+1)
		}
		if len(out.triangles) >= 20000 {
			return
		}
	}
}

func valid3MFMesh(mesh model3MFMesh) bool {
	for _, vertex := range mesh.Vertices {
		if math.IsNaN(vertex.X) || math.IsNaN(vertex.Y) || math.IsNaN(vertex.Z) || math.IsInf(vertex.X, 0) || math.IsInf(vertex.Y, 0) || math.IsInf(vertex.Z, 0) {
			return false
		}
	}
	return len(mesh.Vertices) > 0 && len(mesh.Triangles) > 0
}
