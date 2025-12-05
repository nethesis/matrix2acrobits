package models

// MappingRequest defines the payload used by the Message-to-Matrix mapping API.
type MappingRequest struct {
	Number     int    `json:"number"`
	MatrixID   string `json:"matrix_id,omitempty"`
	UserName   string `json:"user_name,omitempty"`
	SubNumbers []int  `json:"sub_numbers,omitempty"`
}

// MappingResponse is returned once a mapping has been created or looked up.
type MappingResponse struct {
	Number     int    `json:"number"`
	MatrixID   string `json:"matrix_id"`
	UserName   string `json:"user_name,omitempty"`
	SubNumbers []int  `json:"sub_numbers,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}
