package models

// FileTransferContentType is the MIME type for Acrobits file transfer messages.
const FileTransferContentType = "application/x-acro-filetransfer+json"

// FileTransferMessage represents the Acrobits file transfer format.
// This is used when Content-Type is application/x-acro-filetransfer+json.
// See: https://doc.acrobits.net/api/client/x-acro-filetransfer.html
type FileTransferMessage struct {
	// Body is an optional text message applying to the attachment(s).
	Body string `json:"body,omitempty"`
	// Attachments is the array of individual attachment dictionaries.
	Attachments []Attachment `json:"attachments"`
}

// Attachment represents a single file attachment in the Acrobits file transfer format.
type Attachment struct {
	// ContentType is optional. If not present, image/jpeg is assumed.
	ContentType string `json:"content-type,omitempty"`
	// ContentURL is mandatory. It is the location from where to download the data.
	ContentURL string `json:"content-url"`
	// ContentSize is optional. Used to decide whether to download automatically.
	ContentSize int64 `json:"content-size,omitempty"`
	// Filename is optional. Original filename on the sending device.
	Filename string `json:"filename,omitempty"`
	// Description is optional. Text for the particular attachment (not used so far).
	Description string `json:"description,omitempty"`
	// EncryptionKey is optional. Hex-encoded AES128/192/256 CTR key for decryption.
	EncryptionKey string `json:"encryption-key,omitempty"`
	// Hash is optional. CRC32 digest of the decrypted binary data.
	Hash string `json:"hash,omitempty"`
	// Preview is optional. Low quality representation of the data to be downloaded.
	Preview *AttachmentPreview `json:"preview,omitempty"`
}

// AttachmentPreview represents a preview image for an attachment.
type AttachmentPreview struct {
	// ContentType is optional. If not present, image/jpeg is assumed.
	ContentType string `json:"content-type,omitempty"`
	// Content is mandatory. BASE64 representation of the preview image.
	Content string `json:"content"`
}

// IsImageContentType checks if the content type is an image type.
func IsImageContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff":
		return true
	default:
		return false
	}
}

// IsVideoContentType checks if the content type is a video type.
func IsVideoContentType(contentType string) bool {
	switch contentType {
	case "video/mp4", "video/webm", "video/ogg", "video/quicktime", "video/x-msvideo":
		return true
	default:
		return false
	}
}

// IsAudioContentType checks if the content type is an audio type.
func IsAudioContentType(contentType string) bool {
	switch contentType {
	case "audio/mpeg", "audio/mp3", "audio/ogg", "audio/wav", "audio/webm", "audio/aac", "audio/flac":
		return true
	default:
		return false
	}
}
