package store

import (
	"errors"
	"testing"
)

func TestRegistrationPolicy(t *testing.T) {
	for _, tc := range []struct {
		login, password string
		expected        error
	}{
		{"123456", "pa55+!", nil},
		{"English", "abc123", nil},
		{"A1", "AB!cd1", nil},
		{"1", "ABcd+1", nil},
		{"admin", "abc123", ReservedLogin},
		{"AdMiN", "abc123", ReservedLogin},
		{"拼音", "abc123", InvalidRegistrationLogin},
		{"test-user", "abc123", InvalidRegistrationLogin},
		{"test", "123456", InvalidRegistrationPassword},
		{"test", "abcdef", InvalidRegistrationPassword},
		{"test", "12345!", InvalidRegistrationPassword},
		{"test", "abcde!", InvalidRegistrationPassword},
		{"test", "a12+!", InvalidRegistrationPassword},
		{"test", "ab1 cd", InvalidRegistrationPassword},
	} {
		if err := registrationPolicy(tc.login, tc.password); !errors.Is(err, tc.expected) {
			t.Errorf("case for %q with expected %v received %v", tc.login, tc.expected, err)
		}
	}
}
