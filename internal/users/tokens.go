package users

func hexToken(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = digits[v>>4], digits[v&0xf]
	}
	return string(out)
}
