# Large-photo previews and Live Photo playback — October 9, 2026

Production diagnosis found 31 saved preview failures. All 31 originals passed
SHA-256 verification. Twenty-four JPEGs were 33–47 MP and exceeded the
old 32-MP ceiling. Two MOV files were approximately 90 and 113 MiB, above the
old 64-MiB transport bound. Five smaller MOV/MP4 files rendered with the preceding
MOV fix but retained earlier terminal failures. An isolated diagnostic with the
updated renderer successfully produced both 320px and 1280px variants for all 31.
The diagnostic preserved originals and emitted only metadata and result counts.

Raster previews now accept up to 128 MP and 128 MiB, including full-resolution
100-MP camera JPEGs. Video posters accept up to 256 MiB. HEIF and other native
formats retain the 64-MiB bound. These are preview bounds, not upload quotas.
Every pipeline still needs an admitted memory reservation, and small hosts can
refuse large previews rather than exhaust memory. The native JPEG helper retains
its 512-MiB address-space and 30-second CPU limits; its coefficient allowance
supports progressive 100-MP JPEGs. Worker admission accounts for coefficients
and seekable video buffers. Source reads beyond the resident reader's limit use
the bounded CLI path. Working encrypted previews keep their existing keys.

A once-per-owner recovery requeues eligible JPEG/video failures when background
preparation resumes after unlock. Explicit pause is honored. Unsupported
formats and successful previews are preserved; corrupt files can still fail
again. The existing Retry failed previews action remains available. Resource
limits return HTTP 413 with a distinct browser message.

Live Photo motion playback loops on demand using **Loop motion** and returns
to the still with **Stop motion**. The open viewer preserves its image/video
stage across upload and metadata refreshes while updating its controls. Before
this fix, a refresh rebuilt the viewer, aborting cold motion requests and
stopping playback. Still photos and videos now share a scrollable zoom canvas
with 50–400% zoom, mouse panning and a Fit reset. The previous video CSS explicitly
disabled scaling. Zoom updates keep the media element and playback intact.
A browser regression reproduced the upload interruption on the old UI.
Switching assets, locking or closing the viewer still tears down media.

Validation passed:

- `make check` (Go tests, vet, race, line limits and JavaScript checks), plus
  targeted preview/recovery/reader race tests.
- Native baseline/progressive 100-MP fixtures, malformed/oversized inputs and
  concurrent bundle rendering, including ASan/UBSan.
- Isolated rendering of all 31 previously failed production originals at
  320px and 1280px, with source size/SHA-256 verification.
- Full Photos browser smoke on Restic and shared storage at 2 CPUs / 4 GiB,
  followed by dedicated zoom, pan, Fit and Live Photo playback checks on both
  backends after the zoom change. Playback and zoom survive an unrelated upload;
  Range requests and frozen guest motion remain functional.
- Portrait MOV poster/playback and viewport fit at desktop, phone portrait and
  phone landscape sizes; live upload retains already decoded grid thumbnails.

Production activation is pending the versioned image smoke and verified data
checkpoint. The image changes must preserve the host's custom Compose settings.
