package desk

import (
	"net/http/httptest"
	"testing"
)

func TestFutureMobileContractRejectedByQueryAndHeader(t *testing.T) {
	h := &Handler{}
	for _, c := range []struct {
		path, header string
		reject       bool
	}{
		{"/api/v1/devices", "", false},
		{"/api/v1/devices?contract_version=1", "", false},
		{"/api/v1/devices", "1", false},
		{"/api/v1/devices?contract_version=1", "1", false},
		{"/api/v1/devices?contract_version=2", "", true},
		{"/api/v1/devices", "2", true},
		{"/api/v1/devices?contract_version=2", "1", true},
		{"/api/v1/devices?contract_version=1", "2", true},
		{"/api/v1/devices?contract_version=1&contract_version=2", "", true},
		{"/api/v1/photos/uploads?contract_version=99", "", true},
		{"/.well-known/weazlcloud?contract_version=2", "", true},
		{"/api/login?contract_version=2", "", false},
	} {
		r := httptest.NewRequest("POST", c.path, nil)
		if c.header != "" {
			r.Header.Set("X-Weazl-Mobile-Contract", c.header)
		}
		w := httptest.NewRecorder()
		if got := h.rejectMobileContract(w, r); got != c.reject {
			t.Errorf("path=%s header=%s rejected=%v", c.path, c.header, got)
		}
		if c.reject && w.Code != 400 {
			t.Fatal("future contract status", w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/mobile/capabilities", nil)
	r.Header.Add("X-Weazl-Mobile-Contract", "1")
	r.Header.Add("X-Weazl-Mobile-Contract", "2")
	if !h.rejectMobileContract(httptest.NewRecorder(), r) {
		t.Fatal("duplicate contract header accepted")
	}
}
