package discordgo

import (
	"strings"
	"testing"
)

// MultipartBodyWithJSON used to dereference nil files (and files without a
// Reader) when building the form: GuildStickerCreate with a nil file
// panicked the caller instead of returning an error.
func TestMultipartBodyWithJSONNilFile(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MultipartBodyWithJSON panicked on nil file: %v", r)
		}
	}()

	if _, _, err := MultipartBodyWithJSON(struct{}{}, []*File{nil}); err == nil {
		t.Fatal("expected error for nil file, got none")
	}
	if _, _, err := MultipartBodyWithJSON(struct{}{}, []*File{{Name: "x.png"}}); err == nil {
		t.Fatal("expected error for file with nil Reader, got none")
	}

	// A well-formed file must still produce a usable multipart body.
	contentType, body, err := MultipartBodyWithJSON(
		map[string]string{"content": "hi"},
		[]*File{{Name: "x.png", ContentType: "image/png", Reader: strings.NewReader("data")}},
	)
	if err != nil {
		t.Fatalf("valid file: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("content type = %q, want multipart/form-data", contentType)
	}
	if !strings.Contains(string(body), "payload_json") || !strings.Contains(string(body), "x.png") {
		t.Fatalf("multipart body missing parts: %q", body)
	}
}
