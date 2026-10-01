package library

import (
	"context"
	"io"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type metadataReadSpy struct {
	Backend
	reads int
	bytes int
	huge  string
}

func (b *metadataReadSpy) Read(ctx context.Context, ref catalog.Reference, w io.Writer) error {
	b.reads++
	if ref.Object == b.huge {
		block := make([]byte, 1<<20)
		block[0], block[1] = 0xff, 0xd8
		for i := 0; i < 100000; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := w.Write(block)
			b.bytes += n
			if err != nil {
				return err
			}
		}
		return nil
	}
	return b.Backend.Read(ctx, ref, w)
}
func TestMetadataSidecarFirstAndBoundedHugeSource(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	_, _ = l.Put(ctx, "Photos/a.jpg", []byte("original"))
	_, _ = l.Put(ctx, "Photos/a.jpg.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`))
	spy := &metadataReadSpy{Backend: l.backend}
	l.backend = spy
	resolver, err := l.newMetadataResolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := l.Metadata(ctx, "Photos/a.jpg")
	result, err := resolver.resolve(ctx, file, false)
	if err != nil || result.Capture == nil || spy.reads != 1 {
		t.Fatal("restored original despite valid sidecar", result, err, spy.reads)
	}
	_, _ = l.Put(ctx, "Photos/huge.jpg", []byte("prefix"))
	file, _ = l.Metadata(ctx, "Photos/huge.jpg")
	file.Size = 1 << 40
	file.Revision++
	if err = l.catalog.Put(file); err != nil {
		t.Fatal(err)
	}
	file, _ = l.Metadata(ctx, file.Path)
	ref, _ := l.capture(file)
	spy.huge = ref.Object
	spy.bytes = 0
	_, _ = resolver.resolve(ctx, file, false)
	if spy.bytes > 4<<20 {
		t.Fatal("unbounded source bytes", spy.bytes)
	}
	reads := spy.reads
	_, _ = l.PhotoNavigationSummary(ctx, PhotoScope{}, "")
	_, _ = l.PhotoNavigation(ctx, PhotoNavigationRequest{At: "2013-04-05"})
	if spy.reads != reads {
		t.Fatal("navigation restored sources")
	}
}
