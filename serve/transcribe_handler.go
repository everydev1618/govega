package serve

import (
	"io"
	"net/http"
)

// maxTranscribeBytes caps dictation uploads. Mirrors Whisper's own 25MB
// file limit — anything bigger would be rejected upstream anyway.
const maxTranscribeBytes = 25 << 20

// handleTranscribe turns a short audio clip into text via the same
// Whisper transcriber that handles Telegram voice notes. The frontend
// chat input's dictation mic records (webm/opus or mp4) and POSTs the
// clip as multipart field "file"; the response is {"text": "..."}.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTranscribeBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: `multipart field "file" is required`})
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "read audio: " + err.Error()})
		return
	}

	t := newDefaultTranscriber()
	if t.apiKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "transcription not configured (set OPENAI_API_KEY)"})
		return
	}
	text, err := t.Transcribe(r.Context(), audio, header.Filename)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}
