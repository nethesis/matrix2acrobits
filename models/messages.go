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

// FetchMessagesResponse matches Acrobits Modern API specification.
type FetchMessagesResponse struct {
	Date         string `json:"date"`
	ReceivedSMSs []SMS  `json:"received_smss"`
	SentSMSs     []SMS  `json:"sent_smss"`
}

// SMS represents a message in the Acrobits Modern API format.
type SMS struct {
	SMSID                   string `json:"sms_id"`
	SendingDate             string `json:"sending_date"`
	Sender                  string `json:"sender,omitempty"`
	Recipient               string `json:"recipient,omitempty"`
	SMSText                 string `json:"sms_text"`
	ContentType             string `json:"content_type,omitempty"`
	DispositionNotification string `json:"disposition_notification,omitempty"`
	Displayed               bool   `json:"displayed,omitempty"`
	StreamID                string `json:"stream_id"`
}

// Message is a helper struct for internal use.

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
