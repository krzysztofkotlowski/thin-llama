package pull

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("thin-llama"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	if err := VerifyFileSHA256(path, "f1e6d897c7199d1bbbbf6e9ad850a15322dc8930f264f6c695862a7b761cb7dd"); err != nil {
		t.Fatalf("VerifyFileSHA256() unexpected error: %v", err)
	}
}

func TestVerifyFileSHA256RejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(path, []byte("thin-llama"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	if err := VerifyFileSHA256(path, "deadbeef"); err == nil {
		t.Fatal("VerifyFileSHA256() expected checksum mismatch")
	}
}
