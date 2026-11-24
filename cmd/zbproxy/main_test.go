package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedClientConfigForms(t *testing.T) {
	entry := `{"Enabled":true,"Bind":"invalid-ip","ServerAddress":"proxy.test","ServerPort":443,"ServerName":"proxy.test","Password":"password"}`
	for _, tc := range []struct{ name, value, want string }{
		{"legacy object", entry, "EncryptionClient[0]: EncryptionClient.Bind"},
		{"multiple clients", "[" + entry + "," + entry + "]", "EncryptionClient[0]: EncryptionClient.Bind"},
		{"empty array", "[]", "at least one client"},
		{"missing", "null", "at least one client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client.json")
			if err := os.WriteFile(path, []byte(`{"EncryptionClient":`+tc.value+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			err := runEncryptedClient(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}
