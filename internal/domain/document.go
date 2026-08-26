package domain

import (
	"encoding/json"
)

type Document struct {
	ID     string
	Entity string
	Raw    json.RawMessage
	Fields map[string]any
}
