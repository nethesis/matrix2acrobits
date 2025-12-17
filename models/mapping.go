package models

// MappingRequest defines the payload used by the Message-to-Matrix mapping API.
type MappingRequest struct {
	Number     int    `json:"number"`
	MatrixID   string `json:"matrix_id,omitempty"`
	SubNumbers []int  `json:"sub_numbers,omitempty"`
}

// MappingResponse is returned once a mapping has been created or looked up.
type MappingResponse struct {
	Number     int    `json:"number"`
	MatrixID   string `json:"matrix_id"`
	SubNumbers []int  `json:"sub_numbers,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

// LoginRequest represents the payload sent to the external /login endpoint.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents the response from the external /login endpoint.
type LoginResponse struct {
	Token string `json:"token"`
}

// ChatResponse represents the response from the external /chat endpoint.
type ChatResponse struct {
	Matrix ChatMatrixConfig `json:"matrix"`
	Users  []ChatUser       `json:"users,omitempty"`
}

// ChatMatrixConfig contains Matrix homeserver and acrobits URL configuration
type ChatMatrixConfig struct {
	BaseURL     string `json:"base_url"`
	AcrobitsURL string `json:"acrobits_url"`
}

// ChatUser represents a user in the chat configuration response
type ChatUser struct {
	UserName      string   `json:"user_name"`
	MainExtension string   `json:"main_extension"`
	SubExtensions []string `json:"sub_extensions"`
}

// JWTClaims holds custom claims from the JWT token
type JWTClaims struct {
	NethvoiceCTIChat bool `json:"nethvoice_cti.chat"`
}
