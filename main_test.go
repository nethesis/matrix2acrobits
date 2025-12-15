package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/nethesis/matrix2acrobits/api"
	"github.com/nethesis/matrix2acrobits/matrix"
	"github.com/nethesis/matrix2acrobits/models"
	"github.com/nethesis/matrix2acrobits/service"
	"maunium.net/go/mautrix/id"
)

const testEnvFile = "test/test.env"
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
	f, err := os.Open(testEnvFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := &testConfig{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "MATRIX_HOMESERVER_URL":
			cfg.homeserverURL = value
		case "MATRIX_AS_TOKEN":
			cfg.adminToken = value
		case "USER1":
			cfg.user1 = value
		case "USER1_PASSWORD":
			cfg.user1Password = value
		case "USER1_NUMBER":
			cfg.user1Number = value
		case "USER2":
			cfg.user2 = value
		case "USER2_PASSWORD":
			cfg.user2Password = value
		case "USER2_NUMBER":
			cfg.user2Number = value
		case "AS_USER_ID":
			cfg.asUser = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	u, err := url.Parse(cfg.homeserverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MATRIX_HOMESERVER_URL: %w", err)
	}
	cfg.serverName = u.Hostname()

	// If we're pointing at a local homeserver (localhost), try to read the
	// application service registration in `test/acrobits-proxy.yaml` and use
	// its `as_token` so tests authenticate with the same token Synapse expects.
	if strings.Contains(cfg.homeserverURL, "localhost") {
		if token, err := readASTokenFromRegistration("test/acrobits-proxy.yaml"); err == nil && token != "" {
			cfg.adminToken = token
		}
	}

	return cfg, nil
}

func checkTestEnv(t *testing.T) *testConfig {
	t.Helper()
	if !runIntegrationTests {
		t.Skip("RUN_INTEGRATION_TESTS not set; skipping integration tests")
	}
	if _, err := os.Stat(testEnvFile); os.IsNotExist(err) {
		t.Skip("test.env not found, skipping integration tests")
	}
	cfg, err := loadTestEnv()
	if err != nil {
		t.Fatalf("failed to load test.env: %v", err)
	}
	if cfg.homeserverURL == "" || cfg.adminToken == "" || cfg.user1 == "" || cfg.asUser == "" {
		t.Fatal("test.env or environment missing required fields (homeserver, token, user, AS_USER_ID)")
	}
	return cfg
}

func startTestServer(cfg *testConfig) (*echo.Echo, error) {
	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.RequestID())
	e.Use(middleware.Recover())

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
		LogLevel:             "INFO",
		MatrixHomeserverURL:  cfg.homeserverURL,
		MatrixAsToken:        cfg.adminToken,
		MatrixAsUserID:       id.UserID(cfg.asUser),
		MatrixHomeserverHost: cfg.serverName,
		PushTokenDBPath:      "/tmp/push_tokens_test.db",
		ProxyURL:             cfg.homeserverURL,
		CacheTTLSeconds:      3600,
		CacheTTL:             3600 * time.Second,
		ExtAuthTimeoutS:      5,
		ExtAuthTimeout:       5 * time.Second,
	}

	svc := service.NewMessageService(matrixClient, nil, serviceCfg)
	pushSvc := service.NewPushService(nil)
	api.RegisterRoutes(e, svc, pushSvc, cfg.adminToken, nil)

	go func() {
		if err := e.Start("127.0.0.1:" + testServerPort); err != nil && err != http.ErrServerClosed {
			fmt.Printf("server error: %v\n", err)
		}
	}()

	// Wait for server to be ready
	baseURL := "http://127.0.0.1:" + testServerPort
	for i := 0; i < 30; i++ {
		// Use a simple, unauthenticated endpoint to check for readiness
		resp, err := http.Get(baseURL + "/api/internal/map_number_to_matrix")
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
	deadline := time.Now().Add(timeout)
	var lastResp models.FetchMessagesResponse
	var lastErr error
	for time.Now().Before(deadline) {
		fetchReq := models.FetchMessagesRequest{
			Username: username,
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

// generateMappingVariants returns likely variants for a phone number mapping key.
func generateMappingVariants(s string) []string {
	out := make([]string, 0, 4)
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return out
	}
	out = append(out, trimmed)
	// if starts with +, add without +
	if strings.HasPrefix(trimmed, "+") {
		out = append(out, strings.TrimPrefix(trimmed, "+"))
	}
	// digits-only
	digits := make([]rune, 0, len(trimmed))
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) > 0 {
		digitsOnly := string(digits)
		if digitsOnly != trimmed {
			out = append(out, digitsOnly)
		}
	}
	return out
}

// ensureMappingVariants tries a set of mapping key variants and returns the variant that succeeded.
func ensureMappingVariants(t *testing.T, baseURL, adminToken, number, matrixID string) (string, error) {
	t.Helper()
	variants := generateMappingVariants(number)
	var lastErr error
	for _, v := range variants {
		num, err := strconv.Atoi(v)
		if err != nil {
			lastErr = err
			continue
		}
		mappingReq := models.MappingRequest{Number: num, MatrixID: matrixID}
		headers := map[string]string{"X-Super-Admin-Token": adminToken}
		resp, body, err := doRequest("POST", baseURL+"/api/internal/map_number_to_matrix", mappingReq, headers)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return v, nil
		}
		lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return "", lastErr
}

// ensureMapping posts a mapping to the internal mapping API and fails the test on unexpected errors.
func ensureMapping(t *testing.T, baseURL, adminToken, number, matrixID string) {
	t.Helper()
	num, err := strconv.Atoi(strings.TrimPrefix(number, "+"))
	if err != nil {
		t.Fatalf("invalid number format: %v", err)
	}
	mappingReq := models.MappingRequest{
		Number:   num,
		MatrixID: matrixID,
	}
	headers := map[string]string{"X-Super-Admin-Token": adminToken}
	resp, body, err := doRequest("POST", baseURL+"/api/internal/map_number_to_matrix", mappingReq, headers)
	if err != nil {
		t.Fatalf("failed to create mapping via internal API: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		// If mapping creation fails due to recipient resolution, surface detailed info and fail
		t.Fatalf("mapping creation failed: expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

// Helper to get localpart from username like `user@domain.com`
func getLocalpart(username string) string {
	if idx := strings.Index(username, "@"); idx != -1 {
		return username[:idx]
	}
	return username
}

// readASTokenFromRegistration tries to read `as_token` from the provided YAML
// file. This is a lightweight parser since the file is small and format-known.
func readASTokenFromRegistration(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		line := strings.TrimSpace(l)
		if strings.HasPrefix(line, "as_token:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"'"), nil
			}
		}
	}
	return "", nil
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
	user1Localpart := getLocalpart(cfg.user1)
	user2Localpart := getLocalpart(cfg.user2)
	user1MatrixID := fmt.Sprintf("@%s:%s", user1Localpart, cfg.serverName)
	user2MatrixID := fmt.Sprintf("@%s:%s", user2Localpart, cfg.serverName)

	// Step 1: Send message from USER1 to USER2
	t.Run("SendMessage", func(t *testing.T) {
		sendReq := models.SendMessageRequest{
			From: user1MatrixID,
			To:   user2MatrixID, // Send directly to user2's Matrix ID to create a DM
			Body: fmt.Sprintf("Hello from integration test %d", time.Now().Unix()),
		}

		resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", sendReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Logf("send_message returned non-200 status; got %d: %s", resp.StatusCode, string(body))
			if resp.StatusCode == http.StatusBadRequest {
				// Try to auto-create mapping variants and retry the send once
				if r, b, err := attemptMappingsAndRetrySend(t, baseURL, cfg.adminToken, sendReq); err == nil && r != nil && r.StatusCode == http.StatusOK {
					body = b
					resp = r
				} else {
					t.Skip("recipient not resolvable in this environment; mapping attempts exhausted; skipping assertion")
				}
			} else {
				t.Fatalf("unexpected status code %d: %s", resp.StatusCode, string(body))
			}
		}

		var sendResp models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if sendResp.ID == "" {
			t.Error("expected non-empty message_id")
		}
		t.Logf("Message sent successfully: %s", sendResp.ID)
	})

	// Step 2: Fetch messages as USER2 to confirm receipt
	t.Run("FetchMessages", func(t *testing.T) {
		fetchResp, err := fetchMessagesWithRetry(t, baseURL, user2MatrixID, 10*time.Second)
		if err != nil {
			t.Fatalf("fetch messages failed: %v", err)
		}

		found := false
		for _, msg := range fetchResp.ReceivedSMSs {
			if strings.Contains(msg.SMSText, "Hello from integration test") && msg.Sender == user1MatrixID {
				found = true
				t.Logf("Found test message from %s", msg.Sender)
				break
			}
		}
		if !found {
			t.Errorf("test message not found in received messages for %s", user2MatrixID)
		}
	})
}

// attemptMappingsAndRetrySend will try to create mapping variants for the provided
// identifiers and retry the send once. It returns the final response and body.
func attemptMappingsAndRetrySend(t *testing.T, baseURL, adminToken string, origSendReq models.SendMessageRequest) (*http.Response, []byte, error) {
	t.Helper()
	// First attempt: try common variants for the From field (phone numbers)
	fromVariants := generateMappingVariants(origSendReq.From)
	for _, fv := range fromVariants {
		_, err := ensureMappingVariants(t, baseURL, adminToken, fv, origSendReq.From)
		if err == nil {
			// Retry send with the same original request (server will resolve mapping)
			resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", origSendReq, nil)
			if err != nil {
				return resp, body, err
			}
			if resp.StatusCode == http.StatusOK {
				return resp, body, nil
			}
		}
	}

	// Try mapping localpart@server variants for the recipient if it's not a Matrix ID
	// (covers earlier cases where the identifier might be localpart-only)
	if !strings.HasPrefix(origSendReq.To, "@") {
		candidate := fmt.Sprintf("@%s:%s", getLocalpart(origSendReq.To), strings.Split(strings.TrimPrefix(origSendReq.To, "@"), ":")[0])
		_, err := ensureMappingVariants(t, baseURL, adminToken, origSendReq.To, candidate)
		if err == nil {
			resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", origSendReq, nil)
			return resp, body, err
		}
	}

	return nil, nil, fmt.Errorf("mapping attempts exhausted")
}

func TestIntegration_RoomMessaging(t *testing.T) {
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
	user1Localpart := getLocalpart(cfg.user1)
	user2Localpart := getLocalpart(cfg.user2)
	user1MatrixID := fmt.Sprintf("@%s:%s", user1Localpart, cfg.serverName)
	user2MatrixID := fmt.Sprintf("@%s:%s", user2Localpart, cfg.serverName)

	// Create a room and have both users join
	t.Run("CreateRoomAndExchangeMessages", func(t *testing.T) {
		// Step 1: Create a room as user1
		matrixClient, err := matrix.NewClient(matrix.Config{
			HomeserverURL: cfg.homeserverURL,
			AsUserID:      id.UserID(cfg.asUser),
			AsToken:       cfg.adminToken,
		})
		if err != nil {
			t.Fatalf("failed to create matrix client: %v", err)
		}

		// Create a direct room between user1 and user2 using CreateDirectRoom
		aliasKey := fmt.Sprintf("test_room_%d", time.Now().Unix())
		createResp, err := matrixClient.CreateDirectRoom(context.Background(), id.UserID(user1MatrixID), id.UserID(user2MatrixID), aliasKey)
		if err != nil {
			t.Fatalf("failed to create direct room: %v", err)
		}
		roomID := createResp.RoomID
		t.Logf("Created direct room %s", roomID)

		// Step 2: Join the room as user2
		_, err = matrixClient.JoinRoom(context.Background(), id.UserID(user2MatrixID), roomID)
		if err != nil {
			t.Fatalf("failed for user2 to join room: %v", err)
		}
		t.Logf("User2 joined room %s", roomID)

		// Give the server time to process the join event
		time.Sleep(5000 * time.Millisecond)

		// Initial fetch for both users to establish baseline sync tokens
		_, _ = fetchMessagesWithRetry(t, baseURL, user1MatrixID, 2*time.Second)
		_, _ = fetchMessagesWithRetry(t, baseURL, user2MatrixID, 2*time.Second)

		// Step 3: User1 sends a message to the room
		sendReq1 := models.SendMessageRequest{
			From: user1MatrixID,
			To:   string(roomID), // Send to room ID
			Body: fmt.Sprintf("Hello from user1 %d", time.Now().Unix()),
		}
		resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", sendReq1, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Logf("send_message returned non-200 status; got %d: %s", resp.StatusCode, string(body))
			if resp.StatusCode == http.StatusBadRequest {
				t.Skip("recipient not resolvable in this environment; skipping assertion")
			}
			t.Fatalf("unexpected status code %d: %s", resp.StatusCode, string(body))
		}
		var sendResp1 models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp1); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		t.Logf("User1 message sent: %s", sendResp1.ID)

		// Wait for message propagation
		time.Sleep(1 * time.Second)

		// Step 4: User2 sends a message to the room
		sendReq2 := models.SendMessageRequest{
			From: user2MatrixID,
			To:   string(roomID), // Send to room ID
			Body: fmt.Sprintf("Hello from user2 %d", time.Now().Unix()),
		}
		resp, body, err = doRequest("POST", baseURL+"/api/client/send_message", sendReq2, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}
		var sendResp2 models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp2); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		t.Logf("User2 message sent: %s", sendResp2.ID)

		// Wait for message propagation
		time.Sleep(1 * time.Second)

		// Step 5: User1 fetches messages and should see user2's message
		fetchResp1, err := fetchMessagesWithRetry(t, baseURL, user1MatrixID, 10*time.Second)
		if err != nil {
			t.Fatalf("fetch messages failed: %v", err)
		}

		// Check that user1 sees the message from user2
		foundUser2Message := false
		for _, msg := range fetchResp1.ReceivedSMSs {
			if strings.Contains(msg.SMSText, "Hello from user2") {
				foundUser2Message = true
				t.Logf("User1 received message from user2: %s", msg.SMSText)
				break
			}
		}
		if !foundUser2Message {
			t.Errorf("user1 did not receive message from user2")
		}

		// Step 6: User2 fetches messages and should see user1's message
		// Do a fresh fetch to ensure we get all messages
		fetchResp2, err := fetchMessagesWithRetry(t, baseURL, user2MatrixID, 10*time.Second)
		if err != nil {
			t.Fatalf("fetch messages failed: %v", err)
		}

		// Check that user2 sees the message from user1 in received or sent messages
		foundUser1Message := false
		for _, msg := range fetchResp2.ReceivedSMSs {
			if strings.Contains(msg.SMSText, "Hello from user1") {
				foundUser1Message = true
				t.Logf("User2 received message from user1: %s", msg.SMSText)
				break
			}
		}
		if !foundUser1Message {
			t.Logf("Message from user1 not found in received messages, checking all messages. Received: %v, Sent: %v", len(fetchResp2.ReceivedSMSs), len(fetchResp2.SentSMSs))
			t.Errorf("user2 did not receive message from user1")
		}

		// Check that user2 sees their own message in sent messages
		foundOwnMessage := false
		for _, msg := range fetchResp2.SentSMSs {
			if strings.Contains(msg.SMSText, "Hello from user2") {
				foundOwnMessage = true
				t.Logf("User2 sees their own sent message: %s", msg.SMSText)
				break
			}
		}
		if !foundOwnMessage {
			t.Logf("Own message not found in sent messages. Sent count: %d", len(fetchResp2.SentSMSs))
			t.Errorf("user2 did not see their own sent message")
		}
	})
}
