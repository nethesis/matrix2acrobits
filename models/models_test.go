package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSendMessageRequest_Marshal(t *testing.T) {
	req := SendMessageRequest{
		From:        "@user:example.com",
		Password:    "secret",
		To:          "@recipient:example.com",
		Body:        "Hello World",
		ContentType: "text/plain",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify it can be unmarshaled back
	var req2 SendMessageRequest
	err = json.Unmarshal(data, &req2)
	assert.NoError(t, err)
	assert.Equal(t, req.From, req2.From)
	assert.Equal(t, req.To, req2.To)
	assert.Equal(t, req.Body, req2.Body)
}

func TestFetchMessagesRequest_Marshal(t *testing.T) {
	req := FetchMessagesRequest{
		Username:   "@user:example.com",
		Password:   "secret",
		LastID:     "s123",
		LastSentID: "s124",
		Device:     "mobile",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var req2 FetchMessagesRequest
	err = json.Unmarshal(data, &req2)
	assert.NoError(t, err)
	assert.Equal(t, req.Username, req2.Username)
	assert.Equal(t, req.LastID, req2.LastID)
}

func TestMappingRequest_Marshal(t *testing.T) {
	req := MappingRequest{
		Number:   1234567890,
		MatrixID: "@user:example.com",
		UserName: "Test User",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var req2 MappingRequest
	err = json.Unmarshal(data, &req2)
	assert.NoError(t, err)
	assert.Equal(t, req.Number, req2.Number)
	assert.Equal(t, req.MatrixID, req2.MatrixID)
	assert.Equal(t, req.UserName, req2.UserName)
}

func TestMessage_Marshal(t *testing.T) {
	msg := Message{
		ID:          "$event123",
		SendingDate: "2025-01-01T00:00:00Z",
		Sender:      "@user:example.com",
		Recipient:   "!room:example.com",
		Text:        "Test message",
		ContentType: "m.text",
		StreamID:    "!room:example.com",
	}

	data, err := json.Marshal(msg)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	var msg2 Message
	err = json.Unmarshal(data, &msg2)
	assert.NoError(t, err)
	assert.Equal(t, msg.ID, msg2.ID)
	assert.Equal(t, msg.Text, msg2.Text)
	assert.Equal(t, msg.Sender, msg2.Sender)
}

func TestSendMessageResponse_Marshal(t *testing.T) {
	resp := SendMessageResponse{
		ID: "$event123",
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var resp2 SendMessageResponse
	err = json.Unmarshal(data, &resp2)
	assert.NoError(t, err)
	assert.Equal(t, resp.ID, resp2.ID)
}

func TestFetchMessagesResponse_Marshal(t *testing.T) {
	resp := FetchMessagesResponse{
		Date: "2025-01-01T00:00:00Z",
		ReceivedSMSs: []SMS{
			{
				SMSID:   "$event1",
				SMSText: "Received message",
			},
		},
		SentSMSs: []SMS{
			{
				SMSID:   "$event2",
				SMSText: "Sent message",
			},
		},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var resp2 FetchMessagesResponse
	err = json.Unmarshal(data, &resp2)
	assert.NoError(t, err)
	assert.Equal(t, resp.Date, resp2.Date)
	assert.Len(t, resp2.ReceivedSMSs, 1)
	assert.Len(t, resp2.SentSMSs, 1)
}

func TestMappingResponse_Marshal(t *testing.T) {
	resp := MappingResponse{
		Number:    1234567890,
		MatrixID:  "@user:example.com",
		UserName:  "Test User",
		UpdatedAt: "2025-01-01T00:00:00Z",
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var resp2 MappingResponse
	err = json.Unmarshal(data, &resp2)
	assert.NoError(t, err)
	assert.Equal(t, resp.Number, resp2.Number)
	assert.Equal(t, resp.UpdatedAt, resp2.UpdatedAt)
}
