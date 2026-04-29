package service

import (
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"chatgpt2api/internal/storage"

	"github.com/HugoSmits86/nativewebp"
)

const (
	ThumbnailSize         = 720
	thumbnailCacheVersion = 2
	thumbnailExtension    = ".webp"
)

type ImageConfig interface {
	ImagesDir() string
	ImageThumbnailsDir() string
	ImageMetadataDir() string
	CleanupOldImages() int
}

type ImageAccessScope struct {
	OwnerID string
	All     bool
}

type ImageService struct {
	config ImageConfig
	store  storage.JSONDocumentBackend
}

func NewImageService(config ImageConfig, backend ...storage.Backend) *ImageService {
	return &ImageService{config: config, store: firstJSONDocumentStore(backend)}
}

func (s *ImageService) ListImages(baseURL, startDate, endDate string, scope ImageAccessScope) map[string]any {
	s.config.CleanupOldImages()
	root := s.config.ImagesDir()
	items := make([]map[string]any, 0)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		parts := strings.Split(rel, "/")
		day := info.ModTime().Format("2006-01-02")
		if len(parts) >= 4 {
			day = strings.Join(parts[:3], "-")
		}
		if startDate != "" && day < startDate {
			return nil
		}
		if endDate != "" && day > endDate {
			return nil
		}
		ownerID := s.imageOwner(rel)
		if !scope.All && (scope.OwnerID == "" || ownerID != scope.OwnerID) {
			return nil
		}
		thumb := s.thumbnailInfo(path, rel)
		item := map[string]any{
			"name":       filepath.Base(path),
			"path":       rel,
			"date":       day,
			"size":       info.Size(),
			"url":        publicAssetURL(baseURL, "images", rel),
			"created_at": info.ModTime().Format("2006-01-02 15:04:05"),
		}
		if ownerID != "" {
			item["owner_id"] = ownerID
		}
		if thumbRel, ok := thumb["thumbnail_rel"].(string); ok && thumbRel != "" {
			item["thumbnail_url"] = publicAssetURL(baseURL, "image-thumbnails", thumbRel)
		} else {
			item["thumbnail_url"] = ""
		}
		item["width"] = thumb["width"]
		item["height"] = thumb["height"]
		items = append(items, item)
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		return strings.Compare(toString(items[i]["created_at"]), toString(items[j]["created_at"])) > 0
	})
	groupMap := map[string][]map[string]any{}
	var order []string
	for _, item := range items {
		day := toString(item["date"])
		if _, ok := groupMap[day]; !ok {
			order = append(order, day)
		}
		groupMap[day] = append(groupMap[day], item)
	}
	groups := make([]map[string]any, 0, len(order))
	for _, day := range order {
		groups = append(groups, map[string]any{"date": day, "items": groupMap[day]})
	}
	return map[string]any{"items": items, "groups": groups}
}

func (s *ImageService) DeleteImages(paths []string, scope ImageAccessScope) (map[string]any, error) {
	if len(paths) == 0 {
		return nil, errors.New("paths is required")
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return nil, err
	}
	thumbnailRoot, err := filepath.Abs(s.config.ImageThumbnailsDir())
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(paths))
	deleted := 0
	missing := 0
	removedPaths := make([]string, 0, len(paths))
	for _, value := range paths {
		rel, err := cleanImageRelativePath(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}

		imagePath := filepath.Join(imageRoot, filepath.FromSlash(rel))
		if !pathInsideRoot(imageRoot, imagePath) {
			return nil, errors.New("invalid image path")
		}
		if !scope.All && (scope.OwnerID == "" || s.imageOwner(rel) != scope.OwnerID) {
			missing++
			continue
		}
		if err := s.removeImageThumbnail(thumbnailRoot, rel); err != nil {
			return nil, err
		}
		if err := s.removeImageOwner(rel); err != nil {
			return nil, err
		}
		info, err := os.Stat(imagePath)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			missing++
		} else if info.IsDir() {
			return nil, errors.New("image path is not a file")
		} else if err := os.Remove(imagePath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			missing++
		} else {
			deleted++
		}

		removeEmptyParentDirs(imageRoot, filepath.Dir(imagePath))
		removedPaths = append(removedPaths, rel)
	}
	return map[string]any{"deleted": deleted, "missing": missing, "paths": removedPaths}, nil
}

func (s *ImageService) RecordImageOwners(values []string, ownerID string) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return
	}
	for _, value := range values {
		rel, err := imageRelativePathFromValue(value)
		if err != nil {
			continue
		}
		imagePath := filepath.Join(imageRoot, filepath.FromSlash(rel))
		if !pathInsideRoot(imageRoot, imagePath) {
			continue
		}
		info, err := os.Stat(imagePath)
		if err != nil || info.IsDir() {
			continue
		}
		_ = s.writeImageOwner(rel, ownerID)
	}
}

func (s *ImageService) EnsureThumbnails(values []string) {
	if len(values) == 0 {
		return
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return
	}
	for _, value := range values {
		rel, err := imageRelativePathFromValue(value)
		if err != nil {
			continue
		}
		imagePath := filepath.Join(imageRoot, filepath.FromSlash(rel))
		if !pathInsideRoot(imageRoot, imagePath) {
			continue
		}
		info, err := os.Stat(imagePath)
		if err != nil || info.IsDir() {
			continue
		}
		s.ensureThumbnail(imagePath, rel)
	}
}

func (s *ImageService) SourceImageRelativePathFromThumbnail(thumbnailRel string) (string, error) {
	return sourceImageRelativePathFromThumbnail(thumbnailRel)
}

func (s *ImageService) EnsureThumbnail(thumbnailRel string) error {
	sourceRel, err := s.SourceImageRelativePathFromThumbnail(thumbnailRel)
	if err != nil {
		return err
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return err
	}
	sourcePath := filepath.Join(imageRoot, filepath.FromSlash(sourceRel))
	if !pathInsideRoot(imageRoot, sourcePath) {
		return errors.New("invalid image path")
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("image path is not a file")
	}
	thumb := s.ensureThumbnail(sourcePath, sourceRel)
	if toString(thumb["thumbnail_rel"]) == "" {
		return errors.New("thumbnail unavailable")
	}
	return nil
}

func (s *ImageService) thumbnailInfo(sourcePath, rel string) map[string]any {
	thumbPath := s.thumbnailPath(rel)
	thumbRel := thumbnailRelativePath(s.config.ImageThumbnailsDir(), thumbPath)
	result := map[string]any{"thumbnail_rel": thumbRel}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return result
	}
	if thumbInfo, err := os.Stat(thumbPath); err == nil && !thumbInfo.ModTime().Before(sourceInfo.ModTime()) {
		meta := s.readThumbnailMetadata(rel, thumbPath+".json", sourceInfo.ModTime())
		if isCurrentThumbnailMetadata(meta) {
			for key, value := range meta {
				result[key] = value
			}
		}
	}
	return result
}

func (s *ImageService) ensureThumbnail(sourcePath, rel string) map[string]any {
	thumbPath := s.thumbnailPath(rel)
	metaPath := thumbPath + ".json"
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return map[string]any{}
	}
	if thumbInfo, err := os.Stat(thumbPath); err == nil && !thumbInfo.ModTime().Before(sourceInfo.ModTime()) {
		meta := s.readThumbnailMetadata(rel, metaPath, sourceInfo.ModTime())
		if isCurrentThumbnailMetadata(meta) {
			result := map[string]any{"thumbnail_rel": thumbnailRelativePath(s.config.ImageThumbnailsDir(), thumbPath)}
			for key, value := range meta {
				result[key] = value
			}
			return result
		}
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return map[string]any{}
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return map[string]any{}
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	thumb := resizeToFit(flattenImage(img), ThumbnailSize, ThumbnailSize)
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
		return map[string]any{}
	}
	out, err := os.Create(thumbPath)
	if err != nil {
		return map[string]any{}
	}
	encodeErr := nativewebp.Encode(out, thumb, nil)
	closeErr := out.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(thumbPath)
		return map[string]any{}
	}
	_ = s.writeThumbnailMetadata(rel, metaPath, map[string]any{
		"width":             width,
		"height":            height,
		"thumbnail_size":    ThumbnailSize,
		"thumbnail_version": thumbnailCacheVersion,
	})
	return map[string]any{"thumbnail_rel": thumbnailRelativePath(s.config.ImageThumbnailsDir(), thumbPath), "width": width, "height": height}
}

func (s *ImageService) thumbnailPath(rel string) string {
	return filepath.Join(s.config.ImageThumbnailsDir(), filepath.FromSlash(rel)+thumbnailExtension)
}

func (s *ImageService) imageOwner(rel string) string {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return ""
	}
	if s.store != nil {
		raw, err := s.store.LoadJSONDocument(imageOwnerDocumentName(rel))
		if err == nil {
			if meta, ok := raw.(map[string]any); ok {
				return strings.TrimSpace(toString(meta["owner_id"]))
			}
		}
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return strings.TrimSpace(toString(meta["owner_id"]))
}

func (s *ImageService) writeImageOwner(rel, ownerID string) error {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return err
	}
	value := map[string]any{
		"owner_id":   ownerID,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if s.store != nil {
		return s.store.SaveJSONDocument(imageOwnerDocumentName(rel), value)
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	return writeJSONFile(metaPath, value)
}

func (s *ImageService) removeImageOwner(rel string) error {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return err
	}
	if s.store != nil {
		return s.store.DeleteJSONDocument(imageOwnerDocumentName(rel))
	}
	removeErr := os.Remove(metaPath)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	removeEmptyParentDirs(s.config.ImageMetadataDir(), filepath.Dir(metaPath))
	return nil
}

func (s *ImageService) imageOwnerMetadataPath(rel string) (string, error) {
	rel, err := cleanImageRelativePath(rel)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.config.ImageMetadataDir())
	if err != nil {
		return "", err
	}
	metaPath := filepath.Join(root, filepath.FromSlash(rel)+".json")
	if !pathInsideRoot(root, metaPath) {
		return "", errors.New("invalid image path")
	}
	return metaPath, nil
}

func (s *ImageService) readThumbnailMetadata(rel, metaPath string, sourceMtime time.Time) map[string]any {
	if s.store != nil {
		raw, err := s.store.LoadJSONDocument(thumbnailMetadataDocumentName(rel))
		if err == nil {
			if meta, ok := raw.(map[string]any); ok && meta["width"] != nil && meta["height"] != nil {
				return meta
			}
		}
	}
	return readImageMetadata(metaPath, sourceMtime)
}

func (s *ImageService) writeThumbnailMetadata(rel, metaPath string, value map[string]any) error {
	if s.store != nil {
		return s.store.SaveJSONDocument(thumbnailMetadataDocumentName(rel), value)
	}
	return writeJSONFile(metaPath, value)
}

func (s *ImageService) removeImageThumbnail(root, rel string) error {
	if s.store != nil {
		if err := s.store.DeleteJSONDocument(thumbnailMetadataDocumentName(rel)); err != nil {
			return err
		}
	}
	return removeImageThumbnail(root, rel)
}

func imageOwnerDocumentName(rel string) string {
	return "image_metadata/" + filepath.ToSlash(rel) + ".json"
}

func thumbnailMetadataDocumentName(rel string) string {
	return "image_thumbnails/" + filepath.ToSlash(rel) + ".webp.json"
}

func sourceImageRelativePathFromThumbnail(value string) (string, error) {
	thumbnailRel, err := cleanImageRelativePath(value)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(thumbnailRel, thumbnailExtension) {
		return "", errors.New("invalid thumbnail path")
	}
	return cleanImageRelativePath(strings.TrimSuffix(thumbnailRel, thumbnailExtension))
}

func thumbnailRelativePath(root, thumbPath string) string {
	rel, err := filepath.Rel(root, thumbPath)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func publicAssetURL(baseURL, prefix, rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.Trim(prefix, "/") + "/" + strings.Join(parts, "/")
}

func cleanImageRelativePath(value string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(value))
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", errors.New("invalid image path")
	}
	if path.Clean(rel) != rel {
		return "", errors.New("invalid image path")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", errors.New("invalid image path")
		}
	}
	return rel, nil
}

func imageRelativePathFromValue(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", errors.New("invalid image path")
	}
	if parsed, err := url.Parse(text); err == nil {
		pathValue := parsed.EscapedPath()
		if pathValue == "" {
			pathValue = parsed.Path
		}
		if parsed.Scheme != "" || strings.HasPrefix(pathValue, "/") {
			const imagePrefix = "/images/"
			index := strings.Index(pathValue, imagePrefix)
			if index < 0 {
				return "", errors.New("invalid image path")
			}
			rel, err := url.PathUnescape(pathValue[index+len(imagePrefix):])
			if err != nil {
				return "", errors.New("invalid image path")
			}
			return cleanImageRelativePath(rel)
		}
	}
	return cleanImageRelativePath(text)
}

func removeImageThumbnail(root, rel string) error {
	thumbPath := filepath.Join(root, filepath.FromSlash(rel)+".webp")
	if !pathInsideRoot(root, thumbPath) {
		return errors.New("invalid image path")
	}
	removeErr := os.Remove(thumbPath)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	metaErr := os.Remove(thumbPath + ".json")
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}
	removeEmptyParentDirs(root, filepath.Dir(thumbPath))
	return nil
}

func pathInsideRoot(root, target string) bool {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, targetAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func removeEmptyParentDirs(root, start string) {
	current, err := filepath.Abs(start)
	if err != nil {
		return
	}
	for pathInsideRoot(root, current) {
		err := os.Remove(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
		current = filepath.Dir(current)
	}
}

func readImageMetadata(path string, sourceMtime time.Time) map[string]any {
	info, err := os.Stat(path)
	if err != nil || info.ModTime().Before(sourceMtime) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return nil
	}
	if meta["width"] == nil || meta["height"] == nil {
		return nil
	}
	return meta
}

func isCurrentThumbnailMetadata(meta map[string]any) bool {
	return numericMetaValue(meta["thumbnail_version"]) == thumbnailCacheVersion &&
		numericMetaValue(meta["thumbnail_size"]) == ThumbnailSize
}

func numericMetaValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func flattenImage(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, src, b.Min, draw.Over)
	return dst
}

func resizeToFit(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	scale := float64(maxW) / float64(w)
	if sh := float64(maxH) / float64(h); sh < scale {
		scale = sh
	}
	if scale > 1 {
		scale = 1
	}
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		fy := (float64(y)+0.5)*float64(h)/float64(nh) - 0.5
		y0 := int(fy)
		dy := fy - float64(y0)
		if y0 < 0 {
			y0 = 0
			dy = 0
		}
		y1 := y0 + 1
		if y1 >= h {
			y1 = h - 1
		}
		for x := 0; x < nw; x++ {
			fx := (float64(x)+0.5)*float64(w)/float64(nw) - 0.5
			x0 := int(fx)
			dx := fx - float64(x0)
			if x0 < 0 {
				x0 = 0
				dx = 0
			}
			x1 := x0 + 1
			if x1 >= w {
				x1 = w - 1
			}
			dst.Set(x, y, bilinearColor(
				src.At(b.Min.X+x0, b.Min.Y+y0),
				src.At(b.Min.X+x1, b.Min.Y+y0),
				src.At(b.Min.X+x0, b.Min.Y+y1),
				src.At(b.Min.X+x1, b.Min.Y+y1),
				dx,
				dy,
			))
		}
	}
	return dst
}

func bilinearColor(c00, c10, c01, c11 color.Color, dx, dy float64) color.RGBA {
	r00, g00, b00, a00 := c00.RGBA()
	r10, g10, b10, a10 := c10.RGBA()
	r01, g01, b01, a01 := c01.RGBA()
	r11, g11, b11, a11 := c11.RGBA()
	return color.RGBA{
		R: uint8(bilinearChannel(r00, r10, r01, r11, dx, dy) >> 8),
		G: uint8(bilinearChannel(g00, g10, g01, g11, dx, dy) >> 8),
		B: uint8(bilinearChannel(b00, b10, b01, b11, dx, dy) >> 8),
		A: uint8(bilinearChannel(a00, a10, a01, a11, dx, dy) >> 8),
	}
}

func bilinearChannel(c00, c10, c01, c11 uint32, dx, dy float64) uint32 {
	top := float64(c00)*(1-dx) + float64(c10)*dx
	bottom := float64(c01)*(1-dx) + float64(c11)*dx
	return uint32(top*(1-dy) + bottom*dy + 0.5)
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
