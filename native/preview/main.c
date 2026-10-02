// Bounded, single-image JPEG pipe worker. Originals never touch disk.
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <jpeglib.h>
#include <sys/resource.h>

#define MAX_INPUT (64UL << 20)
#define MAX_PIXELS 32000000UL
#define MAX_OUTPUT (8UL << 20)
static size_t bundle_output = 0;

static void fail(void) { fputs("JPEG preview unavailable\n", stderr); exit(1); }
static void jpeg_fail(j_common_ptr c) { (void)c; fail(); }
static void jpeg_message(j_common_ptr c, int level) {
    (void)c;
    // Treat corruption/truncation warnings as failures, never cache partial images.
    if (level < 0) fail();
}

static void limits(void) {
#ifndef WEAZL_SANITIZE
    struct rlimit memory = {512UL << 20, 512UL << 20};
    if (setrlimit(RLIMIT_AS, &memory)) fail();
#endif
    struct rlimit cpu = {30, 30};
    struct rlimit core = {0, 0};
    if (setrlimit(RLIMIT_CPU, &cpu) ||
        setrlimit(RLIMIT_CORE, &core)) fail();
}

static unsigned char *read_input(size_t *length) {
    size_t cap = 65536, n = 0;
    unsigned char *data = malloc(cap);
    if (!data) fail();
    for (;;) {
        size_t got = fread(data + n, 1, cap - n, stdin);
        n += got;
        if (ferror(stdin)) fail();
        if (feof(stdin)) break;
        if (n == cap) {
            if (cap == MAX_INPUT) {
                if (fgetc(stdin) != EOF || ferror(stdin)) fail();
                break;
            }
            cap *= 2;
            unsigned char *next = realloc(data, cap);
            if (!next) fail();
            data = next;
        }
    }
    if (!n) fail();
    *length = n;
    return data;
}

// Bilinear sampling after libjpeg's scaled IDCT keeps output dimensions exact.
static void resize_row(unsigned char *row, const unsigned char *src,
                       unsigned w, unsigned h, unsigned dw, unsigned dh, unsigned y) {
    double sy = dh > 1 ? (double)y * (h - 1) / (dh - 1) : 0;
    unsigned y0 = (unsigned)sy, y1 = y0 + 1 < h ? y0 + 1 : y0;
    double fy = sy - y0;
    for (unsigned x = 0; x < dw; x++) {
        double sx = dw > 1 ? (double)x * (w - 1) / (dw - 1) : 0;
        unsigned x0 = (unsigned)sx, x1 = x0 + 1 < w ? x0 + 1 : x0;
        double fx = sx - x0;
        for (unsigned c = 0; c < 3; c++) {
            double a = src[((size_t)y0*w+x0)*3+c]*(1-fx) + src[((size_t)y0*w+x1)*3+c]*fx;
            double b = src[((size_t)y1*w+x0)*3+c]*(1-fx) + src[((size_t)y1*w+x1)*3+c]*fx;
            row[x*3+c] = (unsigned char)(a*(1-fy)+b*fy+0.5);
        }
    }
}

static void encode_preview(const unsigned char *pixels, unsigned w, unsigned h,
                           unsigned original_w, unsigned original_h, unsigned size, int framed) {
    unsigned dw = size, dh = size;
    if (original_w > original_h) dh = (unsigned)((uint64_t)size*original_h/original_w);
    else dw = (unsigned)((uint64_t)size*original_w/original_h);
    if (!dw) dw = 1;
    if (!dh) dh = 1;
    struct jpeg_compress_struct enc;
    struct jpeg_error_mgr ee;
    enc.err = jpeg_std_error(&ee);
    ee.error_exit = jpeg_fail;
    ee.emit_message = jpeg_message;
    jpeg_create_compress(&enc);
    unsigned char *output = NULL;
    unsigned long output_len = 0;
    jpeg_mem_dest(&enc, &output, &output_len);
    enc.image_width = dw; enc.image_height = dh;
    enc.input_components = 3; enc.in_color_space = JCS_RGB;
    jpeg_set_defaults(&enc);
    jpeg_set_quality(&enc, 82, TRUE);
    jpeg_start_compress(&enc, TRUE);
    unsigned char *row = malloc((size_t)dw*3);
    if (!row) fail();
    while (enc.next_scanline < dh) {
        resize_row(row, pixels, w, h, dw, dh, enc.next_scanline);
        JSAMPROW scan = row;
        if (jpeg_write_scanlines(&enc, &scan, 1) != 1) fail();
    }
    jpeg_finish_compress(&enc);
    if (!output_len || output_len > MAX_OUTPUT) fail();
    if (framed) {
        bundle_output += output_len + 7;
        if (bundle_output > (16UL << 20)) fail();
        unsigned char header[7] = {(unsigned char)(size >> 8), (unsigned char)size, 1,
            (unsigned char)(output_len >> 24), (unsigned char)(output_len >> 16),
            (unsigned char)(output_len >> 8), (unsigned char)output_len};
        if (fwrite(header, 1, sizeof header, stdout) != sizeof header) fail();
    }
    if (fwrite(output, 1, output_len, stdout) != output_len || fflush(stdout)) fail();
    jpeg_destroy_compress(&enc);
    free(output); free(row);
}

int main(int argc, char **argv) {
    if (argc == 2 && strcmp(argv[1], "--version") == 0) {
        puts("weazl-preview-turbo-v1");
        return 0;
    }
    if (argc == 2 && strcmp(argv[1], "--capabilities") == 0) {
        puts("bundle-v1"); return 0;
    }
    int framed = argc == 3 && strcmp(argv[1], "--bundle") == 0;
    const char *spec = framed ? argv[2] : argc == 2 ? argv[1] : "";
    unsigned sizes[8], count = 0;
    long size = 0;
    while (*spec) {
        char *end = NULL;
        long value = strtol(spec, &end, 10);
        if (end == spec || value < 96 || value > 1280 || count == 8 || (*end && (!framed || *end != ','))) fail();
        for (unsigned i = 0; i < count; i++) if (sizes[i] == value) fail();
        sizes[count++] = (unsigned)value;
        if (value > size) size = value;
        if (*end == ',' && !end[1]) fail();
        spec = *end ? end+1 : end;
    }
    if (!count) fail();
    limits();
    size_t length;
    unsigned char *data = read_input(&length);
    struct jpeg_decompress_struct dec;
    struct jpeg_error_mgr de;
    dec.err = jpeg_std_error(&de);
    de.error_exit = jpeg_fail;
    de.emit_message = jpeg_message;
    jpeg_create_decompress(&dec);
    jpeg_mem_src(&dec, data, length);
    if (jpeg_read_header(&dec, TRUE) != JPEG_HEADER_OK) fail();
    unsigned w = dec.image_width, h = dec.image_height;
    if (!w || !h || w > 20000 || h > 20000 || (uint64_t)w*h > MAX_PIXELS) fail();
    unsigned original_w = w, original_h = h;
    unsigned longest = w > h ? w : h;
    dec.scale_num = 1;
    dec.scale_denom = longest >= size*8 ? 8 : longest >= size*4 ? 4 : longest >= size*2 ? 2 : 1;
    dec.out_color_space = JCS_RGB;
    dec.mem->max_memory_to_use = 128UL << 20;
    jpeg_start_decompress(&dec);
    w = dec.output_width; h = dec.output_height;
    if (dec.output_components != 3 || !w || !h || (uint64_t)w*h > MAX_PIXELS) fail();
    unsigned char *pixels = malloc((size_t)w*h*3);
    if (!pixels) fail();
    while (dec.output_scanline < h) {
        JSAMPROW row = pixels + (size_t)dec.output_scanline*w*3;
        if (jpeg_read_scanlines(&dec, &row, 1) != 1) fail();
    }
    jpeg_finish_decompress(&dec);
    jpeg_destroy_decompress(&dec);
    free(data);
    for (unsigned i = 0; i < count; i++)
        encode_preview(pixels, w, h, original_w, original_h, sizes[i], framed);
    if (framed) {
        unsigned char end_frame[7] = {0};
        if (fwrite(end_frame, 1, sizeof end_frame, stdout) != sizeof end_frame || fflush(stdout)) fail();
    }
    free(pixels);
    return 0;
}
