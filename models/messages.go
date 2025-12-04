package models

// SendMessageRequest mirrors the OpenAPI schema for sending a message from Acrobits.
type SendMessageRequest struct {
	From                    string `json:"from"`
	Password                string `json:"password"`
	To                      string `json:"to"`
	Body                    string `json:"body"`
	ContentType             string `json:"content_type"`
	DispositionNotification string `json:"disposition_notification"`
}

// SendMessageResponse reports the Matrix event ID returned to Acrobits.
type SendMessageResponse struct {
	ID string `json:"message_id"`
}

// FetchMessagesRequest mirrors the OpenAPI schema for polling new messages.
type FetchMessagesRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	LastID     string `json:"last_id"`
	LastSentID string `json:"last_sent_id"`
	Device     string `json:"device"`
}

// FetchMessagesResponse is the payload returned from the fetch_messages endpoint.
type FetchMessagesResponse struct {
	Date             string    `json:"date"`
	ReceivedMessages []Message `json:"received_messages"`
	SentMessages     []Message `json:"sent_messages"`
}

// Message represents the shared schema used in both fetch and send responses.
type Message struct {
	ID          string `json:"message_id"`
	SendingDate string `json:"sending_date"`
	Sender      string `json:"sender"`
	Recipient   string `json:"recipient"`
	Text        string `json:"message_text"`
	ContentType string `json:"content_type"`
	StreamID    string `json:"stream_id"`
}

// PushTokenReportRequest mirrors the Acrobits push token reporter POST JSON schema.
type PushTokenReportRequest struct {
	Selector   string `json:"selector"`
	TokenMsgs  string `json:"token_msgs"`
	AppIDMsgs  string `json:"appid_msgs"`
	TokenCalls string `json:"token_calls"`
	AppIDCalls string `json:"appid_calls"`
}

// PushTokenReportResponse is the successful response for push token reporting.
type PushTokenReportResponse struct{}
