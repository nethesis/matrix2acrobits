package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFileTransferContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{
			name:        "exact match",
			contentType: "application/x-acro-filetransfer+json",
			expected:    true,
		},
		{
			name:        "plain text",
			contentType: "text/plain",
			expected:    false,
		},
		{
			name:        "empty",
			contentType: "",
			expected:    false,
		},
		{
			name:        "json",
			contentType: "application/json",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFileTransferContentType(tt.contentType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsImageContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"image/jpeg", true},
		{"image/jpg", true},
		{"image/png", true},
		{"image/gif", true},
		{"image/webp", true},
		{"video/mp4", false},
		{"audio/mpeg", false},
		{"application/pdf", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsImageContentType(tt.contentType))
		})
	}
}

func TestIsVideoContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"video/mp4", true},
		{"video/webm", true},
		{"video/ogg", true},
		{"image/jpeg", false},
		{"audio/mpeg", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsVideoContentType(tt.contentType))
		})
	}
}

func TestIsAudioContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"audio/mpeg", true},
		{"audio/mp3", true},
		{"audio/ogg", true},
		{"audio/wav", true},
		{"image/jpeg", false},
		{"video/mp4", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsAudioContentType(tt.contentType))
		})
	}
}

func TestParseFileTransferMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *FileTransferMessage
		wantErr bool
	}{
		{
			name: "simple image attachment",
			input: `{
				"body": "Check this out",
				"attachments": [{
					"content-type": "image/jpeg",
					"content-url": "https://example.com/image.jpg",
					"content-size": 12345,
					"filename": "photo.jpg"
				}]
			}`,
			want: &FileTransferMessage{
				Body: "Check this out",
				Attachments: []Attachment{
					{
						ContentType: "image/jpeg",
						ContentURL:  "https://example.com/image.jpg",
						ContentSize: 12345,
						Filename:    "photo.jpg",
					},
				},
			},
		},
		{
			name: "attachment with preview",
			input: `{
				"attachments": [{
					"content-url": "https://example.com/video.mp4",
					"content-type": "video/mp4",
					"preview": {
						"content-type": "image/jpeg",
						"content": "BASE64DATA"
					}
				}]
			}`,
			want: &FileTransferMessage{
				Attachments: []Attachment{
					{
						ContentURL:  "https://example.com/video.mp4",
						ContentType: "video/mp4",
						Preview: &AttachmentPreview{
							ContentType: "image/jpeg",
							Content:     "BASE64DATA",
						},
					},
				},
			},
		},
		{
			name: "attachment with encryption",
			input: `{
				"attachments": [{
					"content-url": "https://example.com/encrypted.bin",
					"encryption-key": "F4EC56A83CDA65B2C6DC11E2CF693DAA",
					"hash": "4488649"
				}]
			}`,
			want: &FileTransferMessage{
				Attachments: []Attachment{
					{
						ContentURL:    "https://example.com/encrypted.bin",
						EncryptionKey: "F4EC56A83CDA65B2C6DC11E2CF693DAA",
						Hash:          "4488649",
					},
				},
			},
		},
		{
			name:    "invalid json",
			input:   `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFileTransferMessage(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatrixToAcrobitsAttachment(t *testing.T) {
	tests := []struct {
		name              string
		msgType           string
		body              string
		url               string
		mimeType          string
		filename          string
		size              int64
		thumbnailURL      string
		thumbnailMimeType string
		thumbnailData     []byte
		expected          *Attachment
	}{
		{
			name:     "image message",
			msgType:  "m.image",
			body:     "photo.jpg",
			url:      "mxc://matrix.org/abc123",
			mimeType: "image/jpeg",
			filename: "photo.jpg",
			size:     54321,
			expected: &Attachment{
				ContentType: "image/jpeg",
				ContentURL:  "mxc://matrix.org/abc123",
				ContentSize: 54321,
				Filename:    "photo.jpg",
				Description: "photo.jpg",
			},
		},
		{
			name:              "image with thumbnail",
			msgType:           "m.image",
			body:              "large_image.png",
			url:               "mxc://matrix.org/large",
			mimeType:          "image/png",
			filename:          "large_image.png",
			size:              1000000,
			thumbnailURL:      "mxc://matrix.org/thumb",
			thumbnailMimeType: "image/jpeg",
			expected: &Attachment{
				ContentType: "image/png",
				ContentURL:  "mxc://matrix.org/large",
				ContentSize: 1000000,
				Filename:    "large_image.png",
				Description: "large_image.png",
				Preview: &AttachmentPreview{
					ContentType: "image/jpeg",
					Content:     "mxc://matrix.org/thumb",
				},
			},
		},
		{
			name:     "file with default mime type",
			msgType:  "m.file",
			body:     "document",
			url:      "mxc://matrix.org/doc",
			mimeType: "",
			filename: "",
			size:     0,
			expected: &Attachment{
				ContentType: "application/octet-stream",
				ContentURL:  "mxc://matrix.org/doc",
				Filename:    "document",
				Description: "document",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatrixToAcrobitsAttachment(tt.msgType, tt.body, tt.url, tt.mimeType, tt.filename, tt.size, tt.thumbnailURL, tt.thumbnailMimeType, tt.thumbnailData)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestMatrixMediaToFileTransfer(t *testing.T) {
	ftJSON, err := MatrixMediaToFileTransfer(
		"m.image",
		"vacation.jpg",
		"mxc://matrix.org/vacation",
		"image/jpeg",
		"vacation.jpg",
		98765,
		"",
		"",
	)
	require.NoError(t, err)

	// Parse it back to verify
	var ftMsg FileTransferMessage
	err = json.Unmarshal([]byte(ftJSON), &ftMsg)
	require.NoError(t, err)

	assert.Equal(t, "vacation.jpg", ftMsg.Body)
	require.Len(t, ftMsg.Attachments, 1)
	assert.Equal(t, "mxc://matrix.org/vacation", ftMsg.Attachments[0].ContentURL)
	assert.Equal(t, "image/jpeg", ftMsg.Attachments[0].ContentType)
	assert.Equal(t, "vacation.jpg", ftMsg.Attachments[0].Filename)
	assert.Equal(t, int64(98765), ftMsg.Attachments[0].ContentSize)
}

func TestFileTransferToMatrixEventContent(t *testing.T) {
	tests := []struct {
		name            string
		ftMsg           *FileTransferMessage
		expectedMsgType string
		expectedBody    string
		expectedURL     string
		wantErr         bool
	}{
		{
			name: "image attachment",
			ftMsg: &FileTransferMessage{
				Body: "Nice photo",
				Attachments: []Attachment{
					{
						ContentType: "image/jpeg",
						ContentURL:  "https://example.com/photo.jpg",
						Filename:    "photo.jpg",
						ContentSize: 12345,
					},
				},
			},
			expectedMsgType: "m.image",
			expectedBody:    "photo.jpg",
			expectedURL:     "https://example.com/photo.jpg",
		},
		{
			name: "video attachment",
			ftMsg: &FileTransferMessage{
				Attachments: []Attachment{
					{
						ContentType: "video/mp4",
						ContentURL:  "https://example.com/video.mp4",
						Filename:    "video.mp4",
					},
				},
			},
			expectedMsgType: "m.video",
			expectedBody:    "video.mp4",
			expectedURL:     "https://example.com/video.mp4",
		},
		{
			name: "audio attachment",
			ftMsg: &FileTransferMessage{
				Attachments: []Attachment{
					{
						ContentType: "audio/mpeg",
						ContentURL:  "https://example.com/song.mp3",
						Filename:    "song.mp3",
					},
				},
			},
			expectedMsgType: "m.audio",
			expectedBody:    "song.mp3",
			expectedURL:     "https://example.com/song.mp3",
		},
		{
			name: "generic file attachment",
			ftMsg: &FileTransferMessage{
				Body: "Document attached",
				Attachments: []Attachment{
					{
						ContentType: "application/pdf",
						ContentURL:  "https://example.com/doc.pdf",
						Filename:    "document.pdf",
					},
				},
			},
			expectedMsgType: "m.file",
			expectedBody:    "document.pdf",
			expectedURL:     "https://example.com/doc.pdf",
		},
		{
			name: "default to image/jpeg when no content type",
			ftMsg: &FileTransferMessage{
				Attachments: []Attachment{
					{
						ContentURL: "https://example.com/unknown",
						Filename:   "unknown.jpg",
					},
				},
			},
			expectedMsgType: "m.image",
			expectedBody:    "unknown.jpg",
			expectedURL:     "https://example.com/unknown",
		},
		{
			name:    "nil message",
			ftMsg:   nil,
			wantErr: true,
		},
		{
			name: "no attachments",
			ftMsg: &FileTransferMessage{
				Body:        "Empty",
				Attachments: []Attachment{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgType, content, err := FileTransferToMatrixEventContent(tt.ftMsg)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedMsgType, msgType)
			assert.Equal(t, tt.expectedBody, content["body"])
			assert.Equal(t, tt.expectedURL, content["url"])
		})
	}
}

func TestExtractMatrixMediaInfo(t *testing.T) {
	tests := []struct {
		name                  string
		raw                   map[string]interface{}
		expectedURL           string
		expectedMimeType      string
		expectedFilename      string
		expectedSize          int64
		expectedThumbnailURL  string
		expectedThumbnailMime string
	}{
		{
			name: "full info",
			raw: map[string]interface{}{
				"url":      "mxc://matrix.org/abc123",
				"filename": "test.jpg",
				"info": map[string]interface{}{
					"mimetype":      "image/jpeg",
					"size":          float64(12345),
					"thumbnail_url": "mxc://matrix.org/thumb",
					"thumbnail_info": map[string]interface{}{
						"mimetype": "image/jpeg",
					},
				},
			},
			expectedURL:           "mxc://matrix.org/abc123",
			expectedMimeType:      "image/jpeg",
			expectedFilename:      "test.jpg",
			expectedSize:          12345,
			expectedThumbnailURL:  "mxc://matrix.org/thumb",
			expectedThumbnailMime: "image/jpeg",
		},
		{
			name: "minimal info",
			raw: map[string]interface{}{
				"url": "mxc://matrix.org/minimal",
			},
			expectedURL: "mxc://matrix.org/minimal",
		},
		{
			name:        "empty map",
			raw:         map[string]interface{}{},
			expectedURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, mimeType, filename, size, thumbnailURL, thumbnailMime := ExtractMatrixMediaInfo(tt.raw)
			assert.Equal(t, tt.expectedURL, url)
			assert.Equal(t, tt.expectedMimeType, mimeType)
			assert.Equal(t, tt.expectedFilename, filename)
			assert.Equal(t, tt.expectedSize, size)
			assert.Equal(t, tt.expectedThumbnailURL, thumbnailURL)
			assert.Equal(t, tt.expectedThumbnailMime, thumbnailMime)
		})
	}
}

func TestFileTransferMessageJSONRoundtrip(t *testing.T) {
	original := FileTransferMessage{
		Body: "Test message with attachment",
		Attachments: []Attachment{
			{
				ContentType:   "image/png",
				ContentURL:    "https://example.com/image.png",
				ContentSize:   999999,
				Filename:      "screenshot.png",
				Description:   "A screenshot",
				EncryptionKey: "AABBCCDD",
				Hash:          "12345",
				Preview: &AttachmentPreview{
					ContentType: "image/jpeg",
					Content:     "BASE64ENCODEDDATA",
				},
			},
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Parse back
	parsed, err := ParseFileTransferMessage(string(data))
	require.NoError(t, err)

	// Verify roundtrip
	assert.Equal(t, original.Body, parsed.Body)
	require.Len(t, parsed.Attachments, 1)
	att := parsed.Attachments[0]
	assert.Equal(t, "image/png", att.ContentType)
	assert.Equal(t, "https://example.com/image.png", att.ContentURL)
	assert.Equal(t, int64(999999), att.ContentSize)
	assert.Equal(t, "screenshot.png", att.Filename)
	assert.Equal(t, "A screenshot", att.Description)
	assert.Equal(t, "AABBCCDD", att.EncryptionKey)
	assert.Equal(t, "12345", att.Hash)
	require.NotNil(t, att.Preview)
	assert.Equal(t, "image/jpeg", att.Preview.ContentType)
	assert.Equal(t, "BASE64ENCODEDDATA", att.Preview.Content)
}
