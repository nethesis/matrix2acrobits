package service

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateImagePreview(t *testing.T) {
	t.Run("valid image", func(t *testing.T) {
		// Create a simple test image (100x100 red square)
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		for y := 0; y < 100; y++ {
			for x := 0; x < 100; x++ {
				img.Set(x, y, image.Black)
			}
		}

		// Encode as JPEG
		var buf bytes.Buffer
		err := jpeg.Encode(&buf, img, nil)
		assert.NoError(t, err)

		// Generate preview
		preview, err := GenerateImagePreview(buf.Bytes())
		assert.NoError(t, err)
		assert.NotEmpty(t, preview)

		// Verify it's valid base64
		decoded, err := base64.StdEncoding.DecodeString(preview)
		assert.NoError(t, err)
		assert.NotEmpty(t, decoded)

		// Verify decoded data is a valid JPEG
		_, err = jpeg.Decode(bytes.NewReader(decoded))
		assert.NoError(t, err)
	})

	t.Run("invalid image data", func(t *testing.T) {
		preview, err := GenerateImagePreview([]byte("not an image"))
		assert.Error(t, err)
		assert.Empty(t, preview)
	})

	t.Run("empty data", func(t *testing.T) {
		preview, err := GenerateImagePreview([]byte{})
		assert.Error(t, err)
		assert.Empty(t, preview)
	})
}
