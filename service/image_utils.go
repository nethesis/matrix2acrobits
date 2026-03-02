package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Register PNG decoder

	"github.com/nfnt/resize"
)

const (
	// PreviewMaxWidth is the maximum width for image previews
	PreviewMaxWidth = 120
	// PreviewMaxHeight is the maximum height for image previews
	PreviewMaxHeight = 120
	// PreviewQuality is the JPEG quality for previews (lower = smaller file size)
	PreviewQuality = 30
)

// GenerateImagePreview creates a low-quality thumbnail of an image and returns it as a base64-encoded JPEG.
// If the image cannot be decoded or resized, returns an empty string and an error.
func GenerateImagePreview(imageData []byte) (string, error) {
	// Decode the image
	img, format, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Calculate thumbnail dimensions while preserving aspect ratio
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var thumbWidth, thumbHeight uint
	if width > height {
		thumbWidth = PreviewMaxWidth
		thumbHeight = 0 // resize library will calculate this to preserve aspect ratio
	} else {
		thumbWidth = 0
		thumbHeight = PreviewMaxHeight
	}

	// Resize the image
	thumbnail := resize.Resize(thumbWidth, thumbHeight, img, resize.Lanczos3)

	// Encode as JPEG with low quality
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, thumbnail, &jpeg.Options{Quality: PreviewQuality})
	if err != nil {
		return "", fmt.Errorf("failed to encode thumbnail as JPEG (original format: %s): %w", format, err)
	}

	// Encode to base64
	preview := base64.StdEncoding.EncodeToString(buf.Bytes())
	return preview, nil
}
