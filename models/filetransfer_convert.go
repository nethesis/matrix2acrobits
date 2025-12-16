package models

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// MatrixToAcrobitsAttachment converts Matrix media event content to an Acrobits Attachment.
// It handles m.image, m.video, m.audio, and m.file message types.
func MatrixToAcrobitsAttachment(msgType, body, url, mimeType, filename string, size int64, thumbnailURL, thumbnailMimeType string, thumbnailData []byte) *Attachment {
	attachment := &Attachment{
		ContentURL:  url,
		ContentType: mimeType,
		ContentSize: size,
		Filename:    filename,
		Description: body,
	}

	// If filename is empty but body is present, use body as filename for non-text content
	if attachment.Filename == "" && body != "" {
		attachment.Filename = body
	}

	// Set default content type based on message type if not provided
	if attachment.ContentType == "" {
		switch msgType {
		case "m.image":
			attachment.ContentType = "image/jpeg"
		case "m.video":
			attachment.ContentType = "video/mp4"
		case "m.audio":
			attachment.ContentType = "audio/mpeg"
		default:
			attachment.ContentType = "application/octet-stream"
		}
	}

	// Add preview/thumbnail if available
	if thumbnailURL != "" || len(thumbnailData) > 0 {
		preview := &AttachmentPreview{}
		if thumbnailMimeType != "" {
			preview.ContentType = thumbnailMimeType
		} else {
			preview.ContentType = "image/jpeg"
		}
		// If we have thumbnail data, encode it as base64
		// If we only have a URL, store the URL in Content field (client will need to fetch it)
		if len(thumbnailData) > 0 {
			preview.Content = base64.StdEncoding.EncodeToString(thumbnailData)
		} else if thumbnailURL != "" {
			// Store URL as a special marker for clients to fetch
			// This is a workaround since Acrobits expects base64 content
			preview.Content = thumbnailURL
		}
		attachment.Preview = preview
	}

	return attachment
}

// MatrixMediaToFileTransfer converts a Matrix media message to an Acrobits FileTransferMessage.
// Returns the JSON-encoded file transfer message suitable for sms_text field.
func MatrixMediaToFileTransfer(msgType, body, url, mimeType, filename string, size int64, thumbnailURL, thumbnailMimeType string) (string, error) {
	attachment := MatrixToAcrobitsAttachment(msgType, body, url, mimeType, filename, size, thumbnailURL, thumbnailMimeType, nil)

	ftMsg := &FileTransferMessage{
		Body:        body,
		Attachments: []Attachment{*attachment},
	}

	jsonData, err := json.Marshal(ftMsg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal file transfer message: %w", err)
	}

	return string(jsonData), nil
}

// ParseFileTransferMessage parses a JSON-encoded file transfer message.
func ParseFileTransferMessage(data string) (*FileTransferMessage, error) {
	var ftMsg FileTransferMessage
	if err := json.Unmarshal([]byte(data), &ftMsg); err != nil {
		return nil, fmt.Errorf("failed to parse file transfer message: %w", err)
	}
	return &ftMsg, nil
}

// FileTransferToMatrixEventContent converts an Acrobits file transfer message to Matrix event content.
// Returns the message type, body, and a map suitable for Matrix event content.
// For messages with multiple attachments, only the first attachment is used (Matrix doesn't support multiple files in one event).
func FileTransferToMatrixEventContent(ftMsg *FileTransferMessage) (msgType string, content map[string]interface{}, err error) {
	if ftMsg == nil || len(ftMsg.Attachments) == 0 {
		return "", nil, fmt.Errorf("file transfer message has no attachments")
	}

	// Use the first attachment
	att := ftMsg.Attachments[0]

	// Determine Matrix message type based on content type
	contentType := att.ContentType
	if contentType == "" {
		contentType = "image/jpeg" // Default per Acrobits spec
	}

	switch {
	case IsImageContentType(contentType):
		msgType = "m.image"
	case IsVideoContentType(contentType):
		msgType = "m.video"
	case IsAudioContentType(contentType):
		msgType = "m.audio"
	default:
		msgType = "m.file"
	}

	// Build the message body
	body := att.Filename
	if body == "" {
		body = ftMsg.Body
	}
	if body == "" {
		body = "attachment"
	}

	// Build Matrix event content
	content = map[string]interface{}{
		"msgtype": msgType,
		"body":    body,
		"url":     att.ContentURL,
	}

	// Add info block
	info := map[string]interface{}{}
	if contentType != "" {
		info["mimetype"] = contentType
	}
	if att.ContentSize > 0 {
		info["size"] = att.ContentSize
	}

	// Add thumbnail info if available
	if att.Preview != nil && att.Preview.Content != "" {
		// If preview content looks like a URL, use it as thumbnail_url
		if strings.HasPrefix(att.Preview.Content, "http://") || strings.HasPrefix(att.Preview.Content, "https://") || strings.HasPrefix(att.Preview.Content, "mxc://") {
			info["thumbnail_url"] = att.Preview.Content
			if att.Preview.ContentType != "" {
				info["thumbnail_info"] = map[string]interface{}{
					"mimetype": att.Preview.ContentType,
				}
			}
		}
		// Note: Base64 preview content cannot be directly used in Matrix events
		// The client would need to upload it first
	}

	if len(info) > 0 {
		content["info"] = info
	}

	// Add filename if available
	if att.Filename != "" {
		content["filename"] = att.Filename
	}

	return msgType, content, nil
}

// IsFileTransferContentType checks if the content type is the Acrobits file transfer type.
func IsFileTransferContentType(contentType string) bool {
	return contentType == FileTransferContentType
}

// ExtractMatrixMediaInfo extracts media information from Matrix event content.
// Returns the content URL, mimetype, filename, size, thumbnail URL, and thumbnail mimetype.
func ExtractMatrixMediaInfo(raw map[string]interface{}) (url, mimeType, filename string, size int64, thumbnailURL, thumbnailMimeType string) {
	// Extract URL
	if u, ok := raw["url"].(string); ok {
		url = u
	}

	// Extract filename
	if f, ok := raw["filename"].(string); ok {
		filename = f
	}

	// Extract info block
	if info, ok := raw["info"].(map[string]interface{}); ok {
		if mt, ok := info["mimetype"].(string); ok {
			mimeType = mt
		}
		if s, ok := info["size"].(float64); ok {
			size = int64(s)
		}
		// Extract thumbnail info
		if tu, ok := info["thumbnail_url"].(string); ok {
			thumbnailURL = tu
		}
		if ti, ok := info["thumbnail_info"].(map[string]interface{}); ok {
			if tm, ok := ti["mimetype"].(string); ok {
				thumbnailMimeType = tm
			}
		}
	}

	return
}
