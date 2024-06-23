package helper

import "encoding/json"

type ServerResponse struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func GenerateSuccessResponse(payload interface{}, message string) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	s := &ServerResponse{
		Status:  true,
		Message: message,
		Data:    data,
	}

	res, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	return res, nil
}
