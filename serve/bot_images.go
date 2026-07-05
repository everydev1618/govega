package serve

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/everydev1618/govega/llm"
)

// maxImageBytes bounds a single downloaded image. Anthropic's vision API caps
// images around 5MB; we allow a little headroom and reject anything larger.
const maxImageBytes = 6 << 20

// maxImagesPerTurn bounds how many images we attach to one message.
const maxImagesPerTurn = 8

// imagePlaceholder is the text stored in chat history for a turn that carried
// images (we pass image bytes to the model for the turn but don't persist
// them). Keeps the dashboard thread readable.
func imagePlaceholder(n int) string {
	if n == 1 {
		return "[📎 image] "
	}
	return fmt.Sprintf("[📎 %d images] ", n)
}

// normalizeImageMediaType maps a Content-Type to a media type the vision API
// accepts, or "" if it isn't a supported image.
func normalizeImageMediaType(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return ct
	case "image/jpg":
		return "image/jpeg"
	}
	return ""
}

// chatImagePayload is the wire shape for an image uploaded via the web chat
// API: a base64-encoded payload plus its media type (the frontend strips the
// "data:...;base64," prefix before sending).
type chatImagePayload struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// chatImagesToBlocks validates uploaded image payloads into vision content
// blocks, dropping any with an unsupported type, empty/oversized data, and
// capping the count. Base64 expands ~4/3, so the byte cap maps to ~len*3/4.
func chatImagesToBlocks(payloads []chatImagePayload) []llm.ContentBlock {
	var out []llm.ContentBlock
	for _, p := range payloads {
		mt := normalizeImageMediaType(p.MediaType)
		if mt == "" || p.Data == "" {
			continue
		}
		if len(p.Data)*3/4 > maxImageBytes {
			continue
		}
		out = append(out, llm.ContentBlock{Type: llm.BlockImage, MediaType: mt, Data: p.Data})
		if len(out) >= maxImagesPerTurn {
			break
		}
	}
	return out
}

// downloadImageBlock fetches an image URL and returns a vision content block,
// or nil if it isn't a supported image, is empty, exceeds maxImageBytes, or
// fails to download. mediaTypeHint, when non-empty, is used instead of the
// response Content-Type (Telegram's file server doesn't set a reliable one;
// Discord attachments carry their own). Best-effort: a bad attachment is
// skipped, not fatal.
func downloadImageBlock(ctx context.Context, client *http.Client, url, mediaTypeHint string) *llm.ContentBlock {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}
	mt := normalizeImageMediaType(mediaTypeHint)
	if mt == "" {
		mt = normalizeImageMediaType(resp.Header.Get("Content-Type"))
	}
	if mt == "" {
		return nil
	}
	// Read one byte past the cap so we can detect (and reject) oversized images.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		return nil
	}
	return &llm.ContentBlock{
		Type:      llm.BlockImage,
		MediaType: mt,
		Data:      base64.StdEncoding.EncodeToString(data),
	}
}
