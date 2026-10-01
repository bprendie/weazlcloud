package photos

import (
	"encoding/binary"
	"strings"
)

func parseJPEGCamera(raw []byte) string {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return ""
	}
	for pos := 2; pos+4 <= len(raw); {
		if raw[pos] != 0xff {
			pos++
			continue
		}
		for pos < len(raw) && raw[pos] == 0xff {
			pos++
		}
		if pos >= len(raw) || raw[pos] == 0xda || raw[pos] == 0xd9 {
			break
		}
		marker := raw[pos]
		pos++
		if pos+2 > len(raw) {
			break
		}
		size := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
		if size < 2 || pos+size > len(raw) {
			break
		}
		segment := raw[pos+2 : pos+size]
		if marker == 0xe1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			if _, ifd, ok := parseTIFFIFD(segment[6:]); ok {
				make := strings.TrimSpace(strings.TrimRight(string(ifd[0x010f]), "\x00"))
				model := strings.TrimSpace(strings.TrimRight(string(ifd[0x0110]), "\x00"))
				if strings.HasPrefix(strings.ToLower(model), strings.ToLower(make)) {
					make = ""
				}
				camera := strings.TrimSpace(make + " " + model)
				if len(camera) <= 120 {
					return camera
				}
			}
		}
		pos += size
	}
	return ""
}
