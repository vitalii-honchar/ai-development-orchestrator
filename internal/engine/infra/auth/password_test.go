package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	t.Parallel()
	hash, err := Hash("test password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Hash("test password")
	if err != nil || other == hash || !strings.HasPrefix(hash, hashPrefix) {
		t.Fatal("hash must use a unique salt and encoded parameters")
	}
	for _, password := range []string{"test password", "wrong password", ""} {
		ok, err := Verify(password, hash)
		if err != nil || ok != (password == "test password") {
			t.Fatalf("verification = %v, %v", ok, err)
		}
	}
	for _, malformed := range []string{
		"",
		"plaintext",
		hash + "$extra",
		strings.Replace(hash, "v=19", "v=18", 1),
		strings.Replace(hash, "m=65536", "m=999999999", 1),
		hashPrefix + strings.Repeat("!", 22) + "$" + strings.Repeat("!", 43),
		hashPrefix + "short$short",
	} {
		if ok, err := Verify("test password", malformed); ok || err == nil {
			t.Fatal("malformed hash accepted")
		}
	}
}
