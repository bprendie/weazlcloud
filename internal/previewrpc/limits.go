package previewrpc

// InputLimit bounds decoded-source transport, not stored file size. Video
// posters need a seekable movie index, so ordinary phone clips get a larger
// allowance. Each renderer still admits its complete memory budget separately.
func InputLimit(media string) int {
	if media == "video" {
		return 256 << 20
	}
	if media == "raster" {
		return 128 << 20
	}
	return 64 << 20
}
