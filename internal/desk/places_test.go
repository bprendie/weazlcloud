package desk

import "testing"

func TestValidateHostname(t *testing.T) {
	for _, host := range []string{"grab.example.test", "node-1.home", "localhost"} {
		if err := validateHostname(host); err != nil {
			t.Fatalf("valid hostname %q rejected: %v", host, err)
		}
	}
	for _, host := range []string{"https://grab.example.test", "grab.example.test/path", "grab.example.test:443", "-bad.example", "bad-.example", "bad..example", ""} {
		if err := validateHostname(host); err == nil {
			t.Fatalf("invalid hostname %q accepted", host)
		}
	}
}
