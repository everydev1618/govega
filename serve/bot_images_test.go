package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeImageMediaType(t *testing.T) {
	cases := map[string]string{
		"image/png":                "image/png",
		"image/jpeg":               "image/jpeg",
		"image/jpg":                "image/jpeg",
		"IMAGE/PNG":                "image/png",
		"image/webp; charset=x":    "image/webp",
		"image/gif":                "image/gif",
		"application/octet-stream": "",
		"text/html":                "",
		"":                         "",
	}
	for in, want := range cases {
		if got := normalizeImageMediaType(in); got != want {
			t.Errorf("normalizeImageMediaType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImagePlaceholder(t *testing.T) {
	if !strings.Contains(imagePlaceholder(1), "image") {
		t.Error("single placeholder should mention image")
	}
	if !strings.Contains(imagePlaceholder(3), "3") {
		t.Error("multi placeholder should include the count")
	}
}

func TestDownloadImageBlock(t *testing.T) {
	png := "\x89PNG\r\n\x1a\nfake-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(png))
		case "/notimage":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Valid image → block with base64 data + media type.
	blk := downloadImageBlock(context.Background(), srv.Client(), srv.URL+"/img", "")
	if blk == nil || blk.Type != "image" || blk.MediaType != "image/png" || blk.Data == "" {
		t.Fatalf("expected a png image block, got %+v", blk)
	}

	// Non-image → nil.
	if downloadImageBlock(context.Background(), srv.Client(), srv.URL+"/notimage", "") != nil {
		t.Error("non-image content type should yield nil")
	}

	// Hint overrides an unhelpful content type (Telegram case).
	if b := downloadImageBlock(context.Background(), srv.Client(), srv.URL+"/notimage", "image/jpeg"); b == nil {
		t.Error("media-type hint should let a mislabeled response through as image/jpeg")
	}
}

func TestChatImagesToBlocks(t *testing.T) {
	blocks := chatImagesToBlocks([]chatImagePayload{
		{MediaType: "image/png", Data: "aGVsbG8="},
		{MediaType: "text/html", Data: "x"},    // not an image → dropped
		{MediaType: "image/jpeg", Data: ""},    // empty → dropped
		{MediaType: "image/jpg", Data: "YWJj"}, // normalized to image/jpeg
	})
	if len(blocks) != 2 {
		t.Fatalf("expected 2 valid image blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "image" || blocks[0].MediaType != "image/png" {
		t.Errorf("first block wrong: %+v", blocks[0])
	}
	if blocks[1].MediaType != "image/jpeg" {
		t.Errorf("image/jpg should normalize to image/jpeg, got %q", blocks[1].MediaType)
	}
}

func TestIsAudioAttachment(t *testing.T) {
	for ct, want := range map[string]bool{
		"audio/ogg":  true,
		"audio/mpeg": true,
		"AUDIO/WAV":  true,
		"image/png":  false,
		"text/plain": false,
		"":           false,
	} {
		if got := isAudioAttachment(ct); got != want {
			t.Errorf("isAudioAttachment(%q) = %v, want %v", ct, got, want)
		}
	}
}
