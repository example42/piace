package resolve

import (
	"strings"
	"testing"
)

func TestEndpointCredentialsNeverAppearInValidationErrors(t *testing.T) {
	for _, raw := range []string{"https://user:private@example.test", "http://user:private@example.test", "https://user:private@", "https://user:private@example.test/%zz", "https://user:private%zz@example.test"} {
		if _, err := validateHTTPSEndpoint(raw); err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "user:") {
			t.Fatalf("unsafe endpoint result: %v", err)
		}
	}
}
