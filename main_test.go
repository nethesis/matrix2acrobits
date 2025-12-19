package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nethesis/matrix2acrobits/api"
	"github.com/nethesis/matrix2acrobits/db"
	"github.com/nethesis/matrix2acrobits/logger"
	"github.com/nethesis/matrix2acrobits/matrix"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/nethesis/matrix2acrobits/service"
	"maunium.net/go/mautrix/id"
)

const testServerPort = "18080"

type testConfig struct {
	homeserverURL string
	serverName    string
	adminToken    string
	user1         string
	user1Password string
	user1Number   string
	user2         string
	user2Password string
	user2Number   string
	asUser        string
}

var runIntegrationTests bool

func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "" {
		runIntegrationTests = true
	} else {
		runIntegrationTests = false
	}
	os.Exit(m.Run())
}

func loadTestEnv() (*testConfig, error) {
	// Use the values from test/test.env (copied here so tests don't need the file).
	cfg := &testConfig{
		homeserverURL: "http://localhost:8008",
		serverName:    "localhost",
		adminToken:    "admin-token",
		user1:         "giacomo@localhost",
		user1Password: "Giacomo,1234",
		user1Number:   "201",
		user2:         "mario@localhost",
		user2Password: "Mario,1234",
		user2Number:   "202",
		asUser:        "@_acrobits_proxy:localhost",
	}
	return cfg, nil
}

func checkTestEnv(t *testing.T) *testConfig {
	t.Helper()
	if !runIntegrationTests {
		t.Skip("RUN_INTEGRATION_TESTS not set; skipping integration tests")
	}
	cfg, err := loadTestEnv()
	if err != nil {
		t.Fatalf("failed to load test.env: %v", err)
	}
	if cfg.homeserverURL == "" || cfg.adminToken == "" || cfg.user1 == "" || cfg.asUser == "" {
		t.Fatal("hardcoded test config missing required fields (homeserver, token, user, AS_USER_ID)")
	}
	return cfg
}

var mockAuthSrv *mockAuthServer

func startTestServer(cfg *testConfig) (*echo.Echo, error) {
	// Start mock auth server on port 18081
	var err error
	mockAuthSrv, err = startMockAuthServer("18081")
	if err != nil {
		return nil, fmt.Errorf("failed to start mock auth server: %w", err)
	}

	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.RequestID())
	e.Use(middleware.Recover())

	// Initialize package logger to write to stderr during tests
	logger.InitWithWriter(logger.Level("DEBUG"), os.Stderr)

	matrixClient, err := matrix.NewClient(matrix.Config{
		HomeserverURL: cfg.homeserverURL,
		AsUserID:      id.UserID(cfg.asUser),
		AsToken:       cfg.adminToken,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize matrix client: %w", err)
	}

	// Create a Config from the test configuration
	serviceCfg := &service.Config{
		ProxyPort:            "18080",
		LogLevel:             "DEBUG",
		MatrixHomeserverURL:  cfg.homeserverURL,
		MatrixAsToken:        cfg.adminToken,
		MatrixAsUserID:       id.UserID(cfg.asUser),
		MatrixHomeserverHost: cfg.serverName,
		PushTokenDBPath:      "/tmp/push_tokens_test.db",
		ProxyURL:             "http://127.0.0.1:18080",
		CacheTTLSeconds:      3600,
		CacheTTL:             3600 * time.Second,
		ExtAuthURL:           "http://localhost:18081",
		ExtAuthTimeoutS:      5,
		ExtAuthTimeout:       5 * time.Second,
	}

	// Initialize push token database
	var pushTokenDB *db.Database
	pushTokenDB, err = db.NewDatabase(serviceCfg.PushTokenDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize push token database: %w", err)
	}

	svc := service.NewMessageService(matrixClient, pushTokenDB, serviceCfg)
	pushSvc := service.NewPushService(pushTokenDB)
	api.RegisterRoutes(e, svc, pushSvc, cfg.adminToken, pushTokenDB)

	go func() {
		if err := e.Start("127.0.0.1:" + testServerPort); err != nil && err != http.ErrServerClosed {
			fmt.Printf("server error: %v\n", err)
		}
	}()

	// Wait for server to be ready
	baseURL := "http://127.0.0.1:" + testServerPort
	for i := 0; i < 30; i++ {
		// Use a simple endpoint to check for readiness
		resp, err := http.Get(baseURL + "/api/internal/push_tokens")
		if err == nil {
			resp.Body.Close()
			return e, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("server failed to start")
}

func stopTestServer(e *echo.Echo) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Shutdown(ctx)

	// Stop mock auth server
	if mockAuthSrv != nil {
		stopMockAuthServer(mockAuthSrv)
		mockAuthSrv = nil
	}
}

func doRequest(method, url string, body interface{}, headers map[string]string) (*http.Response, []byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}
	return resp, respBody, nil
}

// fetchMessagesWithRetry calls the proxy fetch_messages endpoint repeatedly until
// the response parses successfully or the timeout elapses. It returns the last
// parsed response (may be empty) and any final error.
func fetchMessagesWithRetry(t *testing.T, baseURL, username string, timeout time.Duration) (models.FetchMessagesResponse, error) {
	t.Helper()
	return fetchMessagesWithRetryAndPassword(t, baseURL, username, "", timeout)
}

// fetchMessagesWithRetryAndPassword calls the proxy fetch_messages endpoint repeatedly until
// the response parses successfully or the timeout elapses. It accepts an optional password.
func fetchMessagesWithRetryAndPassword(t *testing.T, baseURL, username, password string, timeout time.Duration) (models.FetchMessagesResponse, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastResp models.FetchMessagesResponse
	var lastErr error
	for time.Now().Before(deadline) {
		fetchReq := models.FetchMessagesRequest{
			Username: username,
			Password: password,
			LastID:   "",
		}
		resp, body, err := doRequest("POST", baseURL+"/api/client/fetch_messages", fetchReq, nil)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("expected 200, got %d: %s", resp.StatusCode, string(body))
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if err := json.Unmarshal(body, &lastResp); err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		return lastResp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("fetch messages timed out after %s", timeout)
	}
	return lastResp, lastErr
}

func TestIntegration_PushTokenReport(t *testing.T) {
	cfg := checkTestEnv(t)

	// This test runs against a live homeserver defined in test.env
	// It requires the homeserver to be configured with the Application Service.
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping integration tests; set RUN_INTEGRATION_TESTS=1 to run.")
	}

	server, err := startTestServer(cfg)
	if err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}
	defer stopTestServer(server)

	baseURL := "http://127.0.0.1:" + testServerPort

	// Step 1: Report push token without password - must fail
	t.Run("ReportPushTokenWithoutPassword", func(t *testing.T) {
		pushTokenReq := models.PushTokenReportRequest{
			UserName:   cfg.user1,
			Selector:   fmt.Sprintf("selector_%d", time.Now().Unix()),
			TokenMsgs:  "test_token_msgs_12345",
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: "test_token_calls_12345",
			AppIDCalls: "com.acrobits.softphone",
			// Password deliberately omitted
		}

		t.Logf("Reporting push token from %s without password", cfg.user1)
		resp, body, err := doRequest("POST", baseURL+"/api/client/push_token_report", pushTokenReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		// Should fail because password is missing
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected push token report to fail due to missing password, got %d", resp.StatusCode)
		}
		t.Logf("Push token report failed as expected due to missing password: %d: %s", resp.StatusCode, string(body))
	})

	// Step 2: Report push token with incorrect password - must fail
	t.Run("ReportPushTokenWithIncorrectPassword", func(t *testing.T) {
		pushTokenReq := models.PushTokenReportRequest{
			UserName:   cfg.user1,
			Password:   "wrong_password",
			Selector:   fmt.Sprintf("selector_%d", time.Now().Unix()),
			TokenMsgs:  "test_token_msgs_67890",
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: "test_token_calls_67890",
			AppIDCalls: "com.acrobits.softphone",
		}

		t.Logf("Reporting push token from %s with incorrect password", cfg.user1)
		resp, body, err := doRequest("POST", baseURL+"/api/client/push_token_report", pushTokenReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		// Should fail because password is incorrect
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected push token report to fail due to incorrect password, got %d", resp.StatusCode)
		}
		t.Logf("Push token report failed as expected due to incorrect password: %d: %s", resp.StatusCode, string(body))
	})

	// Step 3: Report push token with correct credentials - must succeed
	t.Run("ReportPushTokenSuccess", func(t *testing.T) {
		selector := fmt.Sprintf("selector_%d", time.Now().Unix())
		tokenMsgs := fmt.Sprintf("test_token_msgs_%d", time.Now().Unix())

		pushTokenReq := models.PushTokenReportRequest{
			UserName:   cfg.user1,
			Password:   cfg.user1Password,
			Selector:   selector,
			TokenMsgs:  tokenMsgs,
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: fmt.Sprintf("test_token_calls_%d", time.Now().Unix()),
			AppIDCalls: "com.acrobits.softphone",
		}

		t.Logf("Reporting push token from %s with correct credentials", cfg.user1)
		resp, body, err := doRequest("POST", baseURL+"/api/client/push_token_report", pushTokenReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("push_token_report returned non-200 status; got %d: %s", resp.StatusCode, string(body))
		}

		var pushTokenResp models.PushTokenReportResponse
		if err := json.Unmarshal(body, &pushTokenResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		t.Logf("Push token reported successfully for selector: %s", selector)

		// Note: In a real integration test, we could further verify:
		// - Push token was saved to database
		// - Pusher was registered with Matrix homeserver
		// - But that would require accessing the database or Matrix directly
	})

	// Step 4: Report push token for user2 - must succeed
	t.Run("ReportPushTokenForUser2", func(t *testing.T) {
		selector := fmt.Sprintf("selector_user2_%d", time.Now().Unix())

		pushTokenReq := models.PushTokenReportRequest{
			UserName:   cfg.user2,
			Password:   cfg.user2Password,
			Selector:   selector,
			TokenMsgs:  fmt.Sprintf("test_token_msgs_user2_%d", time.Now().Unix()),
			AppIDMsgs:  "com.acrobits.softphone",
			TokenCalls: fmt.Sprintf("test_token_calls_user2_%d", time.Now().Unix()),
			AppIDCalls: "com.acrobits.softphone",
		}

		t.Logf("Reporting push token from %s", cfg.user2)
		resp, body, err := doRequest("POST", baseURL+"/api/client/push_token_report", pushTokenReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("push_token_report returned non-200 status; got %d: %s", resp.StatusCode, string(body))
		}

		var pushTokenResp models.PushTokenReportResponse
		if err := json.Unmarshal(body, &pushTokenResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		t.Logf("Push token reported successfully for %s with selector: %s", cfg.user2, selector)
	})
}

func TestIntegration_SendAndFetchMessages(t *testing.T) {
	cfg := checkTestEnv(t)

	// This test runs against a live homeserver defined in test.env
	// It requires the homeserver to be configured with the Application Service.
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping integration tests; set RUN_INTEGRATION_TESTS=1 to run.")
	}

	server, err := startTestServer(cfg)
	if err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}
	defer stopTestServer(server)

	baseURL := "http://127.0.0.1:" + testServerPort

	// Step 1: Send message from USER1 to USER2
	t.Run("SendMessage", func(t *testing.T) {
		sendReq := models.SendMessageRequest{
			From: cfg.user1,
			To:   cfg.user2,
			Body: fmt.Sprintf("Hello from integration test %d", time.Now().Unix()),
		}

		// First attempt: no password set - must fail
		t.Logf("Sending message from %s to %s without password", cfg.user1, cfg.user2)
		resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", sendReq, nil)
		t.Logf("Initial send_message response: %d: %s", resp.StatusCode, string(body))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected initial send to fail due to missing password, got 200")
		}
		// Accept 401 Unauthorized (missing credentials) or 400 Bad Request as failure modes,
		// but ensure the initial request did indeed fail because no password was provided.
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected initial failure due to missing password, got %d: %s", resp.StatusCode, string(body))
		}
		t.Logf("Initial send failed as expected due to missing password: %d: %s", resp.StatusCode, string(body))

		// Now set the correct password and retry the send
		// (the request struct is expected to include a Password field)
		sendReq.Password = cfg.user1Password

		resp, body, err = doRequest("POST", baseURL+"/api/client/send_message", sendReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("send_message returned non-200 status on retry; got %d: %s", resp.StatusCode, string(body))
		}

		var sendResp models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if sendResp.ID == "" {
			t.Error("expected non-empty message_id")
		}
		t.Logf("Message sent successfully: %s", sendResp.ID)

		// Send a second message from user1 to user2Number (phone number)
		sendReqToNumber := models.SendMessageRequest{
			From:     cfg.user1,
			To:       cfg.user2Number,
			Body:     fmt.Sprintf("Hello to number from integration test %d", time.Now().Unix()),
			Password: cfg.user1Password,
		}

		t.Logf("Sending message from %s to number %s", cfg.user1, cfg.user2Number)
		resp, body, err = doRequest("POST", baseURL+"/api/client/send_message", sendReqToNumber, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("send_message to number returned non-200 status; got %d: %s", resp.StatusCode, string(body))
		}

		var sendRespNumber models.SendMessageResponse
		if err := json.Unmarshal(body, &sendRespNumber); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if sendRespNumber.ID == "" {
			t.Error("expected non-empty message_id for number send")
		}
		t.Logf("Message to number sent successfully: %s", sendRespNumber.ID)
	})

	// Step 2: Fetch messages as USER2 to confirm receipt
	t.Run("FetchMessages", func(t *testing.T) {
		fetchResp, err := fetchMessagesWithRetry(t, baseURL, cfg.user2, 10*time.Second)
		if err != nil {
			t.Fatalf("fetch messages failed: %v", err)
		}

		found := false
		for _, msg := range fetchResp.ReceivedSMSs {
			if strings.Contains(msg.SMSText, "Hello from integration test") && msg.Sender == cfg.user1Number {
				found = true
				t.Logf("Found test message from %s", msg.Sender)
				break
			}
		}
		if !found {
			t.Errorf("test message not found in received messages for %s", cfg.user2)
		}
	})
}

func TestIntegration_SendAndFetchImageMessages(t *testing.T) {
	cfg := checkTestEnv(t)

	// This test runs against a live homeserver defined in test.env
	// It requires the homeserver to be configured with the Application Service.
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping integration tests; set RUN_INTEGRATION_TESTS=1 to run.")
	}

	server, err := startTestServer(cfg)
	if err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}
	defer stopTestServer(server)

	baseURL := "http://127.0.0.1:" + testServerPort

	// Load the test image
	imageData, err := os.ReadFile("test/test.jpg")
	if err != nil {
		t.Fatalf("failed to read test/test.jpg: %v", err)
	}
	t.Logf("Loaded test image: %d bytes", len(imageData))

	// Step 1: Send image from USER1 to USER2 using Matrix API
	t.Run("SendImageViaMatrix", func(t *testing.T) {
		// Create a Matrix client for user1
		user1MatrixID := id.UserID(cfg.user1)

		uploadResp, err := uploadImageToMatrix(t, cfg, imageData)
		if err != nil {
			t.Fatalf("failed to upload image: %v", err)
		}
		t.Logf("Image uploaded to Matrix: %s", uploadResp)

		// Create a room between user1 and user2 and send the image
		roomID, err := getOrCreateDirectRoom(t, cfg, cfg.user1, cfg.user2)
		if err != nil {
			t.Fatalf("failed to get/create direct room: %v", err)
		}
		t.Logf("Direct room created/found: %s", roomID)

		// Join the room as user1 (as admin to ensure it works)
		err = joinRoomAsUser(t, cfg, cfg.user1, roomID)
		if err != nil {
			t.Logf("warning: failed to join room as user1: %v", err)
		} else {
			t.Logf("Successfully joined room as user1")
		}

		// Join the room as user2 so they can receive the message
		// First, get Mario's access token by logging in
		user2Token, err := loginUser(t, cfg, strings.Split(cfg.user2, "@")[0], cfg.user2Password)
		if err != nil {
			t.Logf("warning: failed to get user2 access token: %v", err)
		} else {
			t.Logf("Got user2 access token")

			err = joinRoomAsUserWithToken(t, cfg, user2Token, roomID)
			if err != nil {
				t.Fatalf("failed to join room as user2: %v", err)
			}
			t.Logf("Successfully joined room as user2 with their own token")
		}

		// Also try with admin token as backup
		err = joinRoomAsUser(t, cfg, cfg.user2, roomID)
		if err != nil {
			t.Logf("warning: failed to join room as user2 with admin: %v", err)
		} else {
			t.Logf("Also joined room as user2 with admin token")
		}

		// First send a test text message to verify the room is working
		testResp := map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Test text message before image",
		}
		textURL := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message", cfg.homeserverURL, roomID)
		textBody, _ := json.Marshal(testResp)
		textReq, _ := http.NewRequest("POST", textURL, bytes.NewReader(textBody))
		textReq.Header.Set("Content-Type", "application/json")
		textReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.adminToken))
		textClient := http.Client{Timeout: 10 * time.Second}
		textHttpResp, _ := textClient.Do(textReq)
		textHttpResp.Body.Close()
		t.Logf("Sent test text message to room")

		// Send image message using Matrix API
		eventID, err := sendImageMessage(t, cfg, user1MatrixID, roomID, uploadResp, "Test image message")
		if err != nil {
			t.Fatalf("failed to send image message: %v", err)
		}
		t.Logf("Image message sent successfully to room %s with event ID: %s", roomID, eventID)
	})

	// Step 2: Fetch messages as USER2 and verify image attachment
	t.Run("FetchImageAndVerify", func(t *testing.T) {
		// Give the server a moment to process the message
		time.Sleep(500 * time.Millisecond)

		// Try multiple fetches until we see the image message or timeout
		var imageMsg *models.SMS
		for attempts := 0; attempts < 5; attempts++ {
			fetchResp, err := fetchMessagesWithRetryAndPassword(t, baseURL, cfg.user2, cfg.user2Password, 10*time.Second)
			if err != nil {
				t.Logf("Attempt %d: fetch messages failed: %v", attempts+1, err)
				time.Sleep(200 * time.Millisecond)
				continue
			}

			// Log attempt info
			t.Logf("Attempt %d: Total received messages: %d", attempts+1, len(fetchResp.ReceivedSMSs))

			// Find the image or text message from our test
			for i := range fetchResp.ReceivedSMSs {
				msg := &fetchResp.ReceivedSMSs[i]
				if msg.ContentType == "application/x-acro-filetransfer+json" {
					t.Logf("Attempt %d: Found image message with content type %s", attempts+1, msg.ContentType)
					imageMsg = msg
					break
				}
				if msg.SMSText == "Test text message before image" || msg.SMSText == "Test image message" {
					t.Logf("Attempt %d: Found test message (text): %s", attempts+1, msg.SMSText)
				}
			}

			if imageMsg != nil {
				break
			}

			// Sleep before retry
			if attempts < 4 {
				time.Sleep(300 * time.Millisecond)
			}
		}

		if imageMsg == nil {
			t.Fatalf("no image message found in received messages after multiple attempts")
		}

		// Verify attachment structure
		var ft models.FileTransfer
		err = json.Unmarshal([]byte(imageMsg.SMSText), &ft)
		if err != nil {
			t.Fatalf("failed to unmarshal SMSText as FileTransfer: %v. SMSText: %s", err, imageMsg.SMSText)
		}

		if len(ft.Attachments) == 0 {
			t.Fatalf("image message has no attachments in FileTransfer")
		}

		attachment := ft.Attachments[0]
		t.Logf("Attachment content-type: %s, url: %s, size: %d", attachment.ContentType, attachment.ContentURL, attachment.ContentSize)

		if attachment.ContentType == "" {
			t.Errorf("attachment content-type is empty")
		}

		if attachment.ContentURL == "" {
			t.Errorf("attachment content-url is empty")
		}

		if attachment.ContentSize == 0 {
			t.Errorf("attachment content-size is 0")
		}

		// Verify preview exists
		if attachment.Preview == nil || attachment.Preview.Content == "" {
			t.Errorf("attachment preview is missing or empty")
		}

		// Step 3: Download the image and verify it matches
		t.Run("VerifyImagePreview", func(t *testing.T) {
			// Verify that the attachment has a preview with the correct structure
			if ft.Attachments[0].Preview == nil {
				t.Fatalf("attachment has no preview")
			}

			preview := ft.Attachments[0].Preview
			if preview.ContentType != "image/jpeg" {
				t.Errorf("preview content-type is %s, expected image/jpeg", preview.ContentType)
			}

			if preview.Content == "" {
				t.Errorf("preview content is empty")
			}

			// Decode the base64 preview
			decodedPreview, err := base64.StdEncoding.DecodeString(preview.Content)
			if err != nil {
				t.Errorf("failed to decode preview: %v", err)
			} else if len(decodedPreview) == 0 {
				t.Errorf("decoded preview is empty")
			} else {
				t.Logf("Preview decoded successfully: %d bytes", len(decodedPreview))
			}

			// Verify the attachment structure
			t.Logf("Attachment structure verified: ContentType=%s, ContentURL=%s, ContentSize=%d, HasPreview=%v",
				ft.Attachments[0].ContentType,
				ft.Attachments[0].ContentURL,
				ft.Attachments[0].ContentSize,
				ft.Attachments[0].Preview != nil)
		})
	})
}

// uploadImageToMatrix uploads an image to the Matrix media repository
// and returns the mxc:// URL
func uploadImageToMatrix(t *testing.T, cfg *testConfig, imageData []byte) (string, error) {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/_matrix/media/v3/upload?access_token=%s", cfg.homeserverURL, cfg.adminToken)

	req, err := http.NewRequest("POST", url, bytes.NewReader(imageData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "image/jpeg")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload returned status %d: %s", resp.StatusCode, string(body))
	}

	var uploadResp struct {
		ContentURI string `json:"content_uri"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&uploadResp); err != nil {
		return "", fmt.Errorf("failed to parse upload response: %w", err)
	}

	if uploadResp.ContentURI == "" {
		return "", fmt.Errorf("upload response missing content_uri")
	}

	return uploadResp.ContentURI, nil
}

// getOrCreateDirectRoom gets or creates a direct room between two users
func getOrCreateDirectRoom(t *testing.T, cfg *testConfig, user1, user2 string) (string, error) {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}

	// Convert user IDs to proper Matrix format: extract username and add @ prefix and :homeserver
	// e.g., "giacomo@localhost" -> "@giacomo:localhost"
	formatMatrixUserID := func(u string) string {
		parts := strings.Split(u, "@")
		if len(parts) == 2 {
			return "@" + parts[0] + ":" + parts[1]
		}
		return "@" + u + ":localhost"
	}

	inviteUser := formatMatrixUserID(user2)

	// Try to find existing direct room by querying joined rooms
	// For simplicity, we'll just create a new room
	createReq := map[string]interface{}{
		"preset":    "trusted_private_chat",
		"is_direct": true,
		"invite": []string{
			inviteUser,
		},
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/createRoom", cfg.homeserverURL)
	body, _ := json.Marshal(createReq)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.adminToken))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create room: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create room returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var createResp struct {
		RoomID string `json:"room_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("failed to parse create room response: %w", err)
	}

	return createResp.RoomID, nil
}

// joinRoomAsUser joins a room as a specific user using admin API
func joinRoomAsUser(t *testing.T, cfg *testConfig, userID string, roomID string) error {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/join", cfg.homeserverURL, roomID)
	body := []byte("{}")

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.adminToken))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to join room: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join room returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// loginUser logs in as a user and returns their access token
func loginUser(t *testing.T, cfg *testConfig, username, password string) (string, error) {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}

	loginReq := map[string]interface{}{
		"type":     "m.login.password",
		"user":     username,
		"password": password,
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/login", cfg.homeserverURL)
	body, _ := json.Marshal(loginReq)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", fmt.Errorf("failed to parse login response: %w", err)
	}

	return loginResp.AccessToken, nil
}

// joinRoomAsUserWithToken joins a room as a specific user using their access token
func joinRoomAsUserWithToken(t *testing.T, cfg *testConfig, accessToken, roomID string) error {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/join", cfg.homeserverURL, roomID)
	body := []byte("{}")

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to join room: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join room returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// sendImageMessage sends an image message to a room using Matrix API
func sendImageMessage(t *testing.T, cfg *testConfig, userID id.UserID, roomID string, mxcURL string, text string) (string, error) {
	t.Helper()

	client := http.Client{Timeout: 10 * time.Second}

	// Build image message event
	msgContent := map[string]interface{}{
		"msgtype": "m.image",
		"url":     mxcURL,
		"body":    text,
		"info": map[string]interface{}{
			"size":     65000, // approximate size
			"mimetype": "image/jpeg",
		},
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message", cfg.homeserverURL, roomID)
	body, _ := json.Marshal(msgContent)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cfg.adminToken))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("send message returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var sendResp struct {
		EventID string `json:"event_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&sendResp); err != nil {
		return "", fmt.Errorf("failed to parse send message response: %w", err)
	}

	return sendResp.EventID, nil
}
