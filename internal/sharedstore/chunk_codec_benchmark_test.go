package sharedstore

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func BenchmarkChunkCodec(b *testing.B) {
	for _, size := range []int{512 << 10, 8 << 20} {
		b.Run("encode/repeated/"+benchmarkSize(size), func(b *testing.B) {
			plain := bytes.Repeat([]byte("weazlcloud chunk codec benchmark "), size/32)
			benchmarkEncodeChunk(b, plain)
		})
		b.Run("encode-reuse/repeated/"+benchmarkSize(size), func(b *testing.B) {
			plain := bytes.Repeat([]byte("weazlcloud chunk codec benchmark "), size/32)
			benchmarkEncodeChunkReuse(b, plain)
		})
		b.Run("encode/noise/"+benchmarkSize(size), func(b *testing.B) {
			plain := make([]byte, size)
			if _, err := rand.New(rand.NewSource(9)).Read(plain); err != nil {
				b.Fatal(err)
			}
			benchmarkEncodeChunk(b, plain)
		})
		plain := bytes.Repeat([]byte("weazlcloud chunk codec benchmark "), size/32)
		stored, encoding, err := encodeChunk(plain)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("decode/repeated/"+benchmarkSize(size), func(b *testing.B) {
			b.SetBytes(int64(len(plain)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := decodeChunk(encoding, stored, int64(len(plain)), &benchmarkDiscard{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkEncodeChunk(b *testing.B, plain []byte) {
	b.Helper()
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := encodeChunk(plain); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkSize(size int) string {
	if size < 1<<20 {
		return "512KiB"
	}
	return fmt.Sprintf("%dMiB", size/(1<<20))
}

func benchmarkEncodeChunkReuse(b *testing.B, plain []byte) {
	b.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderCRC(true))
	if err != nil {
		b.Fatal(err)
	}
	defer encoder.Close()
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encoder.EncodeAll(plain, nil)
	}
}

type benchmarkDiscard struct{}

func (*benchmarkDiscard) Write(p []byte) (int, error) { return len(p), nil }
