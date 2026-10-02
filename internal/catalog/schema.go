package catalog

import "encoding/json"

// Schema 2 deliberately changes the files wire shape. An older server fails
// decoding instead of silently dropping collection fields on its next write.
// The envelope remains owner-encrypted; legacy arrays load and upgrade once.
type treeWire tree

func (t tree) MarshalJSON() ([]byte, error) {
	if t.Schema < 2 {
		return json.Marshal(treeWire(t))
	}
	return json.Marshal(struct {
		*treeWire
		Files struct {
			Entries []File `json:"entries"`
		} `json:"files"`
	}{treeWire: (*treeWire)(&t), Files: struct {
		Entries []File `json:"entries"`
	}{Entries: t.Files}})
}
func (t *tree) UnmarshalJSON(raw []byte) error {
	var wire treeWire
	var body struct {
		*treeWire
		Files json.RawMessage `json:"files"`
	}
	body.treeWire = &wire
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	if wire.Schema < 0 || wire.Schema > 2 {
		return ErrCollectionSchema
	}
	if wire.Schema == 2 {
		var files struct {
			Entries []File `json:"entries"`
		}
		if err := json.Unmarshal(body.Files, &files); err != nil {
			return err
		}
		wire.Files = files.Entries
	} else if len(body.Files) > 0 {
		if err := json.Unmarshal(body.Files, &wire.Files); err != nil {
			return err
		}
	}
	*t = tree(wire)
	return nil
}
