package photos

import (
	"encoding/binary"
	"strconv"
	"strings"
)

type exifReader struct {
	raw   []byte
	order binary.ByteOrder
}

func parseJPEGExif(raw []byte) (Capture, error) {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return Capture{}, ErrNoCaptureMetadata
	}
	for pos := 2; pos+4 <= len(raw); {
		if raw[pos] != 0xff {
			pos++
			continue
		}
		for pos < len(raw) && raw[pos] == 0xff {
			pos++
		}
		if pos >= len(raw) {
			break
		}
		marker := raw[pos]
		pos++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if pos+2 > len(raw) {
			break
		}
		size := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
		if size < 2 || pos+size > len(raw) {
			break
		}
		segment := raw[pos+2 : pos+size]
		if marker == 0xe1 && len(segment) > 6 && string(segment[:6]) == "Exif\x00\x00" {
			if capture, ok := parseTIFF(segment[6:]); ok {
				return capture, nil
			}
		}
		pos += size
	}
	return Capture{}, ErrNoCaptureMetadata
}

func parseJPEGOrientation(raw []byte) int {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return 0
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
			if order, ifd, ok := parseTIFFIFD(segment[6:]); ok {
				value, exists := ifd[0x0112]
				if exists && len(value) >= 2 {
					orientation := int(order.Uint16(value[:2]))
					if orientation >= 1 && orientation <= 8 {
						return orientation
					}
				}
			}
		}
		pos += size
	}
	return 0
}

func parseTIFF(raw []byte) (Capture, bool) {
	_, ifd, ok := parseTIFFIFD(raw)
	if !ok {
		return Capture{}, false
	}
	order := ifdOrder(raw)
	date, offset, found := ifd.captureDate(exifReader{raw: raw, order: order})
	if !found {
		return Capture{}, false
	}
	t, err := normalizeWallTime(date, offset)
	if err != nil {
		return Capture{}, false
	}
	return Capture{Time: t, OffsetMinutes: offset, Source: "exif-DateTimeOriginal"}, true
}

func parseTIFFIFD(raw []byte) (binary.ByteOrder, exifIFD, bool) {
	if len(raw) < 8 {
		return nil, nil, false
	}
	var order binary.ByteOrder = binary.BigEndian
	if string(raw[:2]) == "II" {
		order = binary.LittleEndian
	} else if string(raw[:2]) != "MM" {
		return nil, nil, false
	}
	if order.Uint16(raw[2:4]) != 42 {
		return nil, nil, false
	}
	reader := exifReader{raw: raw, order: order}
	ifd, ok := reader.ifd(order.Uint32(raw[4:8]))
	return order, ifd, ok
}

func ifdOrder(raw []byte) binary.ByteOrder {
	if len(raw) >= 2 && string(raw[:2]) == "II" {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

type exifIFD map[uint16][]byte

func (r exifReader) ifd(offset uint32) (exifIFD, bool) {
	start := int(offset)
	if start < 0 || start+2 > len(r.raw) {
		return nil, false
	}
	count := int(r.order.Uint16(r.raw[start : start+2]))
	if count > 512 || start+2+count*12+4 > len(r.raw) {
		return nil, false
	}
	result := make(exifIFD)
	for i := 0; i < count; i++ {
		entry := r.raw[start+2+i*12 : start+14+i*12]
		tag := r.order.Uint16(entry[:2])
		typ := r.order.Uint16(entry[2:4])
		number := r.order.Uint32(entry[4:8])
		size, ok := exifTypeSize(typ)
		if !ok || number > 1<<20 || uint64(size)*uint64(number) > 1<<31 {
			continue
		}
		length := int(uint64(size) * uint64(number))
		location := start + 10 + i*12
		if length > 4 {
			location = int(r.order.Uint32(entry[8:12]))
		}
		if location < 0 || location+length > len(r.raw) {
			continue
		}
		result[tag] = append([]byte(nil), r.raw[location:location+length]...)
	}
	return result, true
}

func exifTypeSize(typ uint16) (int, bool) {
	switch typ {
	case 1, 2, 6, 7:
		return 1, true
	case 3, 8:
		return 2, true
	case 4, 9, 11:
		return 4, true
	case 5, 10, 12:
		return 8, true
	default:
		return 0, false
	}
}

func (i exifIFD) captureDate(r exifReader) (string, *int, bool) {
	dateBytes, ok := i[0x8769]
	if ok && len(dateBytes) >= 4 {
		offset := r.order.Uint32(dateBytes)
		if child, valid := r.ifd(offset); valid {
			if value, exists := child[0x9003]; exists {
				date := strings.TrimRight(string(value), "\x00")
				if offset, exists := child[0x9011]; exists {
					return date, parseOffset(offset), date != ""
				}
				return date, parseOffset(i[0x9011]), date != ""
			}
		}
	}
	if value, exists := i[0x0132]; exists {
		date := strings.TrimRight(string(value), "\x00")
		return date, parseOffset(i[0x9011]), date != ""
	}
	return "", nil, false
}

func parseOffset(raw []byte) *int {
	value := strings.TrimRight(string(raw), "\x00")
	if len(value) != 6 || (value[0] != '+' && value[0] != '-') || value[3] != ':' {
		return nil
	}
	hours, errH := strconv.Atoi(value[1:3])
	minutes, errM := strconv.Atoi(value[4:6])
	if errH != nil || errM != nil || hours > 23 || minutes > 59 {
		return nil
	}
	total := hours*60 + minutes
	if value[0] == '-' {
		total = -total
	}
	return &total
}
