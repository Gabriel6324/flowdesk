package auth_test

import (
	"flowdesk/internal/auth"
	"strings"
	"testing"
)

func TestASCIIAccountIdentifierAndPasswordBoundaries(t *testing.T) {
	v, e := auth.Email("  Name+tag@Example.COM  ")
	if e != nil || v != "name+tag@example.com" {
		t.Fatal(v, e)
	}
	for _, v := range []string{"a..b@example.com", ".a@example.com", "a@.com", "a@example.", "a@-example.com", "a@exam_ple.com", "Name <a@example.com>", "用户@example.com", strings.Repeat("a", 65) + "@example.com"} {
		if _, e := auth.Email(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
	if !auth.PasswordValid(strings.Repeat("界", 12)) || !auth.PasswordValid(" 1234567890 ") || auth.PasswordValid(strings.Repeat("界", 129)) || auth.PasswordValid("12345678901") || auth.PasswordValid("12345678901\x00") {
		t.Fatal("password boundary")
	}
}
