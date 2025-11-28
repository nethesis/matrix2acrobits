package integration

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
	"strings"
	"testing"
	"time"

	"github.com/gsanchietti/matrix2acrobits/internal/api"
	"github.com/gsanchietti/matrix2acrobits/internal/matrix"
	"github.com/gsanchietti/matrix2acrobits/internal/service"
	"github.com/gsanchietti/matrix2acrobits/pkg/models"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"maunium.net/go/mautrix/id"
)

const testEnvFile = "../../test.env"
const testServerPort = "18080"

type testConfig struct {
	homeserverURL string
	serverName    string
	adminToken    string
	asUserID      id.UserID
	user1         string
	user1Password string
	user1Number   string
	user2         string
	user2Password string
	user2Number   string
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
		case "SUPER_ADMIN_TOKEN":
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
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Also load from environment for values not in the file
	cfg.asUserID = id.UserID(os.Getenv("AS_USER_ID"))

	u, err := url.Parse(cfg.homeserverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MATRIX_HOMESERVER_URL: %w", err)
	}
	cfg.serverName = u.Hostname()

	return cfg, nil
}

func checkTestEnv(t *testing.T) *testConfig {
	t.Helper()
	if _, err := os.Stat(testEnvFile); os.IsNotExist(err) {
		t.Skip("test.env not found, skipping integration tests")
	}
	cfg, err := loadTestEnv()
	if err != nil {
		t.Fatalf("failed to load test.env: %v", err)
	}
	if cfg.homeserverURL == "" || cfg.adminToken == "" || cfg.user1 == "" || cfg.asUserID == "" {
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
		AsToken:       cfg.adminToken,
		AsUserID:      cfg.asUserID,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize matrix client: %w", err)
	}

	svc := service.NewMessageService(matrixClient)
	api.RegisterRoutes(e, svc, cfg.adminToken)

	go func() {
		if err := e.Start("127.0.0.1:" + testServerPort); err != nil && err != http.ErrServerClosed {
			fmt.Printf("server error: %v\n", err)
		}
	}()

	// Wait for server to be ready
	baseURL := "http://127.0.0.1:" + testServerPort
	for i := 0; i < 30; i++ {
		// Use a simple, unauthenticated endpoint to check for readiness
		resp, err := http.Get(baseURL + "/api/internal/map_sms_to_matrix")
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

// Helper to get localpart from username like `user@domain.com`
func getLocalpart(username string) string {
	if idx := strings.Index(username, "@"); idx != -1 {
		return username[:idx]
	}
	return username
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
			From:    user1MatrixID,
			SMSTo:   user2MatrixID, // Send directly to user2's Matrix ID to create a DM
			SMSBody: fmt.Sprintf("Hello from integration test %d", time.Now().Unix()),
		}

		resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", sendReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var sendResp models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if sendResp.SMSID == "" {
			t.Error("expected non-empty sms_id")
		}
		t.Logf("Message sent successfully: %s", sendResp.SMSID)
	})

	// Step 2: Fetch messages as USER2 to confirm receipt
	t.Run("FetchMessages", func(t *testing.T) {
		// Wait for message to propagate
		time.Sleep(2 * time.Second)

		fetchReq := models.FetchMessagesRequest{
			Username: user2MatrixID,
			LastID:   "",
		}

		resp, body, err := doRequest("POST", baseURL+"/api/client/fetch_messages", fetchReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var fetchResp models.FetchMessagesResponse
		if err := json.Unmarshal(body, &fetchResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		found := false
		for _, msg := range fetchResp.ReceivedSMSS {
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

func TestIntegration_MappingAPI(t *testing.T) {
	cfg := checkTestEnv(t)
	server, err := startTestServer(cfg)
	if err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}
	defer stopTestServer(server)

	baseURL := "http://127.0.0.1:" + testServerPort
	serverName := cfg.serverName

	t.Run("CreateAndGetMapping", func(t *testing.T) {
		headers := map[string]string{
			"X-Super-Admin-Token": cfg.adminToken,
		}

		mappingReq := models.MappingRequest{
			SMSNumber: "+9998887777",
			MatrixID:  fmt.Sprintf("@testuser:%s", serverName),
			RoomID:    fmt.Sprintf("!testroom:%s", serverName),
		}

		// Create mapping
		resp, body, err := doRequest("POST", baseURL+"/api/internal/map_sms_to_matrix", mappingReq, headers)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		// Retrieve mapping
		resp, body, err = doRequest("GET", baseURL+"/api/internal/map_sms_to_matrix?sms_number=%2B9998887777", nil, headers)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var mappingResp models.MappingResponse
		if err := json.Unmarshal(body, &mappingResp); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		if mappingResp.SMSNumber != "+9998887777" {
			t.Errorf("expected sms_number=+9998887777, got %s", mappingResp.SMSNumber)
		}
	})

	t.Run("UnauthorizedAccess", func(t *testing.T) {
		mappingReq := models.MappingRequest{
			SMSNumber: "+1111111111",
			MatrixID:  "@test:example.com",
			RoomID:    "!test:example.com",
		}

		// No token
		resp, _, err := doRequest("POST", baseURL+"/api/internal/map_sms_to_matrix", mappingReq, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}

		// Wrong token
		headers := map[string]string{
			"X-Super-Admin-Token": "wrongtoken",
		}
		resp, _, err = doRequest("POST", baseURL+"/api/internal/map_sms_to_matrix", mappingReq, headers)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	})
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
			AsUserID:      cfg.asUserID,
			AsToken:       cfg.adminToken,
		})
		if err != nil {
			t.Fatalf("failed to create matrix client: %v", err)
		}

		roomName := fmt.Sprintf("Test Room %d", time.Now().Unix())
		createResp, err := matrixClient.CreateRoom(context.Background(), id.UserID(user1MatrixID), roomName, []id.UserID{id.UserID(user2MatrixID)})
		if err != nil {
			t.Fatalf("failed to create room: %v", err)
		}
		roomID := createResp.RoomID
		t.Logf("Created room %s", roomID)

		// Step 2: Join the room as user2
		_, err = matrixClient.JoinRoom(context.Background(), id.UserID(user2MatrixID), roomID)
		if err != nil {
			t.Fatalf("failed for user2 to join room: %v", err)
		}
		t.Logf("User2 joined room %s", roomID)

		// Step 3: User1 sends a message to the room
		sendReq1 := models.SendMessageRequest{
			From:    user1MatrixID,
			SMSTo:   string(roomID), // Send to room ID
			SMSBody: fmt.Sprintf("Hello from user1 %d", time.Now().Unix()),
		}
		resp, body, err := doRequest("POST", baseURL+"/api/client/send_message", sendReq1, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}
		var sendResp1 models.SendMessageResponse
		if err := json.Unmarshal(body, &sendResp1); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}
		t.Logf("User1 message sent: %s", sendResp1.SMSID)

		// Wait for message propagation
		time.Sleep(1 * time.Second)

		// Step 4: User2 sends a message to the room
		sendReq2 := models.SendMessageRequest{
			From:    user2MatrixID,
			SMSTo:   string(roomID), // Send to room ID
			SMSBody: fmt.Sprintf("Hello from user2 %d", time.Now().Unix()),
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
		t.Logf("User2 message sent: %s", sendResp2.SMSID)

		// Wait for message propagation
		time.Sleep(1 * time.Second)

		// Step 5: User1 fetches messages and should see user2's message
		fetchReq1 := models.FetchMessagesRequest{
			Username: user1MatrixID,
			LastID:   "",
		}
		resp, body, err = doRequest("POST", baseURL+"/api/client/fetch_messages", fetchReq1, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var fetchResp1 models.FetchMessagesResponse
		if err := json.Unmarshal(body, &fetchResp1); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		// Check that user1 sees the message from user2
		foundUser2Message := false
		for _, msg := range fetchResp1.ReceivedSMSS {
			if strings.Contains(msg.SMSText, "Hello from user2") && msg.Sender == user2MatrixID {
				foundUser2Message = true
				t.Logf("User1 received message from user2: %s", msg.SMSText)
				break
			}
		}
		if !foundUser2Message {
			t.Errorf("user1 did not receive message from user2")
		}

		// Step 6: User2 fetches messages and should see both their own and user1's messages
		fetchReq2 := models.FetchMessagesRequest{
			Username: user2MatrixID,
			LastID:   "",
		}
		resp, body, err = doRequest("POST", baseURL+"/api/client/fetch_messages", fetchReq2, nil)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var fetchResp2 models.FetchMessagesResponse
		if err := json.Unmarshal(body, &fetchResp2); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		// Check that user2 sees the message from user1
		foundUser1Message := false
		for _, msg := range fetchResp2.ReceivedSMSS {
			if strings.Contains(msg.SMSText, "Hello from user1") && msg.Sender == user1MatrixID {
				foundUser1Message = true
				t.Logf("User2 received message from user1: %s", msg.SMSText)
				break
			}
		}
		if !foundUser1Message {
			t.Errorf("user2 did not receive message from user1")
		}

		// Check that user2 also sees their own message in sent messages
		foundOwnMessage := false
		for _, msg := range fetchResp2.SentSMSS {
			if strings.Contains(msg.SMSText, "Hello from user2") && msg.Sender == user2MatrixID {
				foundOwnMessage = true
				t.Logf("User2 sees their own sent message: %s", msg.SMSText)
				break
			}
		}
		if !foundOwnMessage {
			t.Errorf("user2 did not see their own sent message")
		}
	})
}
