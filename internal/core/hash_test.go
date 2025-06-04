package core

import (
	"os"
	"path/filepath" // Added import
	"testing"
)

func TestGetFileHash(t *testing.T) {
	// Test 1: Known content
	t.Run("KnownContent", func(t *testing.T) {
		content := []byte("hello world")
		// SHA256 hash for "hello world" (no newline)
		expectedHash := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

		tempFile, err := os.CreateTemp(t.TempDir(), "testfile_known_*.txt")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tempFile.Name()) // Clean up

		_, err = tempFile.Write(content)
		if err != nil {
			tempFile.Close() // Close beforeFatalf
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		filePath := tempFile.Name() // Get name before closing
		if err := tempFile.Close(); err != nil {
			t.Fatalf("Failed to close temp file: %v", err)
		}

		t.Logf("Hashing file '%s' with content '%s'", filePath, string(content))
		hash, err := GetFileHash(filePath)
		if err != nil {
			t.Errorf("GetFileHash() error = %v, wantErr nil", err)
		}
		if hash != expectedHash {
			// Log the actual file content if hash mismatches, to see if it's what we expect
			fileData, readErr := os.ReadFile(filePath)
			if readErr == nil {
				t.Logf("Actual file content being hashed (len %d): [%x] %s", len(fileData), fileData, string(fileData))
			} else {
				t.Logf("Could not read back file content for debug: %v", readErr)
			}
			t.Errorf("GetFileHash() hash = %s, want %s", hash, expectedHash)
		}
	})

	// Test 2: Empty file
	t.Run("EmptyFile", func(t *testing.T) {
		// SHA256 hash for an empty string ""
		expectedHashForEmpty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

		emptyFile, err := os.CreateTemp(t.TempDir(), "testfile_empty_*.txt")
		if err != nil {
			t.Fatalf("Failed to create empty temp file: %v", err)
		}
		defer os.Remove(emptyFile.Name()) // Clean up

		filePath := emptyFile.Name()
		if err := emptyFile.Close(); err != nil {
			t.Fatalf("Failed to close empty temp file: %v", err)
		}

		t.Logf("Hashing empty file '%s'", filePath)
		hash, err := GetFileHash(filePath)
		if err != nil {
			t.Errorf("GetFileHash() for empty file error = %v, wantErr nil", err)
		}
		if hash != expectedHashForEmpty {
			t.Errorf("GetFileHash() for empty file hash = %s, want %s", hash, expectedHashForEmpty)
		}
	})

	// Test 3: Non-existent file
	t.Run("NonExistentFile", func(t *testing.T) {
		nonExistentFilePath := filepath.Join(t.TempDir(), "nonexistentfile.txt") // Ensure it's truly non-existent
		t.Logf("Attempting to hash non-existent file '%s'", nonExistentFilePath)
		_, err := GetFileHash(nonExistentFilePath)
		if err == nil {
			t.Errorf("GetFileHash() for non-existent file error = nil, wantErr non-nil")
		} else {
			t.Logf("Got expected error for non-existent file: %v", err)
		}
	})
}
