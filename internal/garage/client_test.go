/*
Copyright 2026 Raj Singh.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package garage

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestZoneRedundancy_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    ZoneRedundancy
		expected string
	}{
		{
			name:     "Maximum serializes to lowercase string",
			input:    ZoneRedundancy{Maximum: true},
			expected: `"maximum"`,
		},
		{
			name:     "AtLeast serializes to object",
			input:    ZoneRedundancy{AtLeast: intPtr(2)},
			expected: `{"atLeast":2}`,
		},
		{
			name:     "Empty serializes to null",
			input:    ZoneRedundancy{},
			expected: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(result) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(result))
			}
		})
	}
}

func TestZoneRedundancy_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    ZoneRedundancy
		expectError bool
		errorMsg    string
	}{
		{
			name:     "lowercase maximum",
			input:    `"maximum"`,
			expected: ZoneRedundancy{Maximum: true},
		},
		{
			name:     "uppercase Maximum (legacy)",
			input:    `"Maximum"`,
			expected: ZoneRedundancy{Maximum: true},
		},
		{
			name:     "atLeast object",
			input:    `{"atLeast":3}`,
			expected: ZoneRedundancy{AtLeast: intPtr(3)},
		},
		{
			name:     "null value",
			input:    `null`,
			expected: ZoneRedundancy{},
		},
		{
			name:        "invalid string value",
			input:       `"invalid"`,
			expectError: true,
			errorMsg:    "invalid ZoneRedundancy string value",
		},
		{
			name:        "atLeast value too low",
			input:       `{"atLeast":0}`,
			expectError: true,
			errorMsg:    "invalid ZoneRedundancy atLeast value: 0 (must be 1-7)",
		},
		{
			name:        "atLeast value too high",
			input:       `{"atLeast":10}`,
			expectError: true,
			errorMsg:    "invalid ZoneRedundancy atLeast value: 10 (must be 1-7)",
		},
		{
			name:        "object missing atLeast key",
			input:       `{"other":5}`,
			expectError: true,
			errorMsg:    "missing 'atLeast' key",
		},
		{
			name:        "invalid format - array",
			input:       `[1,2,3]`,
			expectError: true,
			errorMsg:    "invalid ZoneRedundancy format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result ZoneRedundancy
			err := json.Unmarshal([]byte(tt.input), &result)

			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errorMsg)
				}
				if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Maximum != tt.expected.Maximum {
				t.Errorf("Maximum: expected %v, got %v", tt.expected.Maximum, result.Maximum)
			}
			if (result.AtLeast == nil) != (tt.expected.AtLeast == nil) {
				t.Errorf("AtLeast nil mismatch: expected %v, got %v", tt.expected.AtLeast, result.AtLeast)
			} else if result.AtLeast != nil && *result.AtLeast != *tt.expected.AtLeast {
				t.Errorf("AtLeast value: expected %v, got %v", *tt.expected.AtLeast, *result.AtLeast)
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsReplicationConstraint(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Garage 500 error with replication factor message",
			err:      &APIError{StatusCode: 500, Message: "The number of nodes with positive capacity (2) is smaller than the replication factor (3)"},
			expected: true,
		},
		{
			name:     "Garage 400 error with replication constraint",
			err:      &APIError{StatusCode: 400, Message: "Cannot apply layout: replication factor requires more nodes"},
			expected: true,
		},
		{
			name:     "Case insensitive matching",
			err:      &APIError{StatusCode: 500, Message: "REPLICATION FACTOR constraint violated"},
			expected: true,
		},
		{
			name:     "Other 500 error",
			err:      &APIError{StatusCode: 500, Message: "Internal server error: database connection failed"},
			expected: false,
		},
		{
			name:     "404 error",
			err:      &APIError{StatusCode: 404, Message: "Not found"},
			expected: false,
		},
		{
			name:     "Non-API error",
			err:      fmt.Errorf("network timeout"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsReplicationConstraint(tt.err)
			if result != tt.expected {
				t.Errorf("IsReplicationConstraint() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
