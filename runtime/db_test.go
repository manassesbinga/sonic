package runtime

import (
	"encoding/json"
	"testing"
)

func TestRedactURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com/api", "http://example.com/api"},
		{"http://example.com/api?user=john", "http://example.com/api?user=john"},
		{"http://example.com/api?token=secret123&user=john", "http://example.com/api?token=[REDACTED]&user=john"},
		{"http://example.com/api?password=pwd&key=123", "http://example.com/api?password=[REDACTED]&key=[REDACTED]"},
		{"http://example.com/api?user_password=pwd", "http://example.com/api?user_password=[REDACTED]"},
		{"http://example.com/api?api%5Fkey=123", "http://example.com/api?api%5Fkey=[REDACTED]"},
	}

	for _, tt := range tests {
		result := redactURL(tt.input)
		if result != tt.expected {
			t.Errorf("redactURL(%q) = %q; expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestRedactJSONOrForm(t *testing.T) {
	t.Run("JSON Object", func(t *testing.T) {
		input := `{"user":"john","password":"my-secret-password","nested":{"token":"sensitivetoken"},"db_password":"dbpass","my_api_key":"apikey"}`
		result := redactJSONOrForm(input)

		var m map[string]interface{}
		if err := json.Unmarshal([]byte(result), &m); err != nil {
			t.Fatalf("failed to parse result JSON: %v", err)
		}

		if m["password"] != "[REDACTED]" {
			t.Errorf("expected redacted password, got %v", m["password"])
		}

		if m["db_password"] != "[REDACTED]" {
			t.Errorf("expected redacted db_password, got %v", m["db_password"])
		}

		if m["my_api_key"] != "[REDACTED]" {
			t.Errorf("expected redacted my_api_key, got %v", m["my_api_key"])
		}

		nested := m["nested"].(map[string]interface{})
		if nested["token"] != "[REDACTED]" {
			t.Errorf("expected redacted nested token, got %v", nested["token"])
		}

		if m["user"] != "john" {
			t.Errorf("user field was incorrectly redacted: %v", m["user"])
		}
	})

	t.Run("JSON Array", func(t *testing.T) {
		input := `[{"user":"john","password":"123"},{"key":"secret"}]`
		result := redactJSONOrForm(input)

		var list []map[string]interface{}
		if err := json.Unmarshal([]byte(result), &list); err != nil {
			t.Fatalf("failed to parse result JSON array: %v", err)
		}

		if list[0]["password"] != "[REDACTED]" {
			t.Errorf("expected redacted password in first array item, got %v", list[0]["password"])
		}
		if list[1]["key"] != "[REDACTED]" {
			t.Errorf("expected redacted key in second array item, got %v", list[1]["key"])
		}
		if list[0]["user"] != "john" {
			t.Errorf("user field in array item was incorrectly redacted")
		}
	})

	t.Run("Form Urlencoded", func(t *testing.T) {
		input := "user=john&password=123&token=abc&user_password=xyz&api%5Fkey=123"
		result := redactJSONOrForm(input)
		expected := "user=john&password=[REDACTED]&token=[REDACTED]&user_password=[REDACTED]&api%5Fkey=[REDACTED]"

		if result != expected {
			t.Errorf("redactJSONOrForm(%q) = %q; expected %q", input, result, expected)
		}
	})

	t.Run("Plaintext Body", func(t *testing.T) {
		input := "Plaintext message without parameters"
		result := redactJSONOrForm(input)
		if result != input {
			t.Errorf("expected plaintext message to remain unchanged")
		}
	})
}
