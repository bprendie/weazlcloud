# JPEG preview helper

Build on a host with libjpeg-turbo development headers:

```sh
cc -O3 -Wall -Wextra -Werror -o weazl-preview-turbo main.c -ljpeg
```

Install in the service's executable search path. Docker builds and installs it
already. The CGO-free service invokes it directly, never through a shell.

Input is one JPEG on stdin; the sole argument is the longest output edge
(96–1280 pixels). Output is one metadata-free JPEG at quality 82 on stdout.
The helper is single threaded; WeazlCloud owns concurrency. libjpeg-turbo chooses
SIMD implementations at runtime. Decode uses 1/2, 1/4 or 1/8 scaling where suitable,
then a scalar bilinear resize. This does not claim SIMD for the resize loop or
AVX-512 support in the codec. No EXIF-orientation behavior is changed in this pass.

Warnings (including truncated JPEGs) fail the image. Grayscale is converted to
RGB; the Go dispatcher retains CMYK handling. Input, dimensions, pixels, output,
CPU, address space and wall time are bounded. The process never opens source or
output files; no libjpeg backing-store implementation is provided by libjpeg-turbo.
Errors exit the child and release its allocations; the Go parent reaps it.

`WEAZL_SANITIZE` is only used by the sanitizer test build to omit RLIMIT_AS,
because AddressSanitizer needs a large virtual address reservation. Production
Docker builds never set it. `JSIMD_FORCENONE=1` is upstream's measurement switch;
use it only for comparisons, not as the production default.

References:
- https://libjpeg-turbo.org/About/SIMDCoverage
- https://github.com/libjpeg-turbo/libjpeg-turbo/blob/main/simd/README.md
