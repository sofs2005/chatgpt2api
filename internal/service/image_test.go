package service

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HugoSmits86/nativewebp"
)

type testImageConfig struct {
	root string
}

func (c testImageConfig) ImagesDir() string {
	path := filepath.Join(c.root, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testImageConfig) ImageThumbnailsDir() string {
	path := filepath.Join(c.root, "image_thumbnails")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testImageConfig) CleanupOldImages() int {
	return 0
}

func TestImageServiceListImagesReturnsEmptyArrays(t *testing.T) {
	service := NewImageService(testImageConfig{root: t.TempDir()})
	result := service.ListImages("http://127.0.0.1:8000", "", "")

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"groups":[],"items":[]}` {
		t.Fatalf("ListImages() JSON = %s", data)
	}
}

func TestImageServiceCreatesWebPThumbnails(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	result := service.ListImages("http://127.0.0.1:8000", "", "")
	items := result["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	if got := toString(items[0]["path"]); got != "2026/04/29/sample.png" {
		t.Fatalf("path = %q, want relative image path", got)
	}
	thumbnailURL := toString(items[0]["thumbnail_url"])
	if !strings.HasSuffix(thumbnailURL, ".webp") {
		t.Fatalf("thumbnail_url = %q, want .webp suffix", thumbnailURL)
	}

	rel := strings.TrimPrefix(thumbnailURL, "http://127.0.0.1:8000/image-thumbnails/")
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), filepath.FromSlash(rel))
	file, err := os.Open(thumbPath)
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}
	defer file.Close()
	decoded, err := nativewebp.Decode(file)
	if err != nil {
		t.Fatalf("decode webp thumbnail: %v", err)
	}
	if decoded.Bounds().Dx() <= 0 || decoded.Bounds().Dy() <= 0 {
		t.Fatalf("decoded thumbnail has invalid bounds: %v", decoded.Bounds())
	}
	if decoded.Bounds().Dx() > ThumbnailSize || decoded.Bounds().Dy() > ThumbnailSize {
		t.Fatalf("decoded thumbnail bounds = %v, want max side <= %d", decoded.Bounds(), ThumbnailSize)
	}
	meta, err := os.ReadFile(thumbPath + ".json")
	if err != nil {
		t.Fatalf("read thumbnail metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(meta, &metadata); err != nil {
		t.Fatalf("unmarshal thumbnail metadata: %v", err)
	}
	if numericMetaValue(metadata["thumbnail_size"]) != ThumbnailSize {
		t.Fatalf("thumbnail_size metadata = %v, want %d", metadata["thumbnail_size"], ThumbnailSize)
	}
}

func TestImageServiceDeleteImagesRemovesOriginalAndThumbnail(t *testing.T) {
	root := t.TempDir()
	config := testImageConfig{root: root}
	imagePath := filepath.Join(config.ImagesDir(), "2026", "04", "29", "sample.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := writeTestPNG(imagePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(config)
	service.ListImages("http://127.0.0.1:8000", "", "")
	thumbPath := filepath.Join(config.ImageThumbnailsDir(), "2026", "04", "29", "sample.png.webp")
	if _, err := os.Stat(thumbPath); err != nil {
		t.Fatalf("thumbnail was not created: %v", err)
	}

	result, err := service.DeleteImages([]string{"2026/04/29/sample.png"})
	if err != nil {
		t.Fatalf("DeleteImages() error = %v", err)
	}
	if result["deleted"] != 1 || result["missing"] != 0 {
		t.Fatalf("DeleteImages() = %#v", result)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("original still exists, stat error = %v", err)
	}
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Fatalf("thumbnail still exists, stat error = %v", err)
	}
	if _, err := os.Stat(thumbPath + ".json"); !os.IsNotExist(err) {
		t.Fatalf("thumbnail metadata still exists, stat error = %v", err)
	}
}

func TestImageServiceDeleteImagesRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outsidePath := filepath.Join(root, "outside.png")
	if err := writeTestPNG(outsidePath); err != nil {
		t.Fatalf("writeTestPNG() error = %v", err)
	}

	service := NewImageService(testImageConfig{root: root})
	if _, err := service.DeleteImages([]string{"../outside.png"}); err == nil {
		t.Fatal("DeleteImages() error = nil, want traversal rejection")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside file was changed: %v", err)
	}
}

func writeTestPNG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 10), B: 120, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
