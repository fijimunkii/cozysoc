package localapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func hasRequestParams(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) > 0
}

func decodeRequiredParams(raw json.RawMessage, destination any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("request params are required")
	}
	return decodeStrictJSON(trimmed, destination)
}
