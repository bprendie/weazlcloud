package sharedstore

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var n byte
	for i := range a {
		n |= a[i] ^ b[i]
	}
	return n == 0
}
