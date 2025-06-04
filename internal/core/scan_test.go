package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	// "fmt" // Removed unused import
)

// Helper to create a file with specific content
func createFile(t *testing.T, path string, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("Failed to create directory %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}
}

// Helper to calculate hash for test setup, panics on error for simplicity in test def
func mustGetHash(t *testing.T, content string) string {
	t.Helper()
	// Create a temporary file to hash the content
	tmpFile, err := os.CreateTemp(t.TempDir(), "hash_content_")
	if err != nil {
		t.Fatalf("Failed to create temp file for hashing: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(content)
	if err != nil {
		tmpFile.Close()
		t.Fatalf("Failed to write content to temp file for hashing: %v", err)
	}
	name := tmpFile.Name()
	tmpFile.Close()

	// Use GetFileHash from the package being tested, assuming it works correctly for this helper.
	// This makes the test for ScanFolder depend on GetFileHash.
	// If GetFileHash has issues (like the newline anomaly), this will propagate.
	// For a pure ScanFolder test, one might mock GetFileHash or use pre-computed hashes
	// that match GetFileHash's actual behavior in the test env.
	// Given previous findings, GetFileHash might add a newline effect.
	// So, we use GetFileHash on a file created by WriteString (which does not add newline).
	// This will test ScanFolder's logic correctly, and any hash discrepancies will be
	// due to GetFileHash's behavior (which is tested separately).

	// To get consistent hashes with GetFileHash's current behavior in the sandbox (which seems to effectively add a newline):
	// We need to ensure the content being hashed by this helper *also* effectively has a newline,
	// OR we accept that the hashes here will be for content-without-newline if GetFileHash is fixed/sandboxed differently.
	// For now, let's assume we want hashes of exact content passed.
	// The anomaly is that GetFileHash(file_with_no_newline) == sha256sum(content_with_newline).
	// So, mustGetHash(content_without_newline) will give hash_of_content_without_newline_as_if_it_had_newline
	// if used with the current GetFileHash in the anomalous environment.
	// This is tricky. Let's use the standard definition of hash for "content" string.
	// The files created by createFile will have exact content.
	// If GetFileHash is anomalous, the test here will reflect that by comparing against
	// hashes of *exact* content.

	// Re-evaluating: For this test, we need to compute hashes exactly as ScanFolder's internal GetFileHash will.
	// So, using GetFileHash itself is the most self-consistent way *for this test's purpose*.
	// The resulting map will have hashes as produced by *this* GetFileHash.
	// The `expectedMap` should also use hashes produced by *this* GetFileHash.

	hash, err := GetFileHash(name)
	if err != nil {
		t.Fatalf("mustGetHash: GetFileHash failed for content '%s': %v", content, err)
	}
	return hash
}


func TestScanFolder(t *testing.T) {
	// Test 1: Basic structure with files, subdir, and .filesync to ignore
	t.Run("BasicScan", func(t *testing.T) {
		baseDir := t.TempDir()
		configDirToSkip := ".filesync" // Standard skip directory

		contentFile1 := "content of file1"
		contentFile2 := "content of file2 in subdir"
		contentIgnored := "this should be ignored"

		createFile(t, filepath.Join(baseDir, "file1.txt"), contentFile1)
		createFile(t, filepath.Join(baseDir, "subdir", "file2.txt"), contentFile2)
		createFile(t, filepath.Join(baseDir, configDirToSkip, "ignoreme.txt"), contentIgnored)
		createFile(t, filepath.Join(baseDir, configDirToSkip, "subconfig", "another.cfg"), contentIgnored)


		// Calculate expected hashes using the same GetFileHash that ScanFolder uses
		// This makes the test self-consistent regarding hash generation.
		// Create temp files for hashing as mustGetHash uses file paths.
		pathFile1 := filepath.Join(baseDir, "file1.txt")
		pathFile2 := filepath.Join(baseDir, "subdir", "file2.txt")

		hashOfFile1, err := GetFileHash(pathFile1)
		if err != nil { t.Fatalf("Failed to get hash for file1.txt for expected map: %v", err)}
		hashOfFile2, err := GetFileHash(pathFile2)
		if err != nil { t.Fatalf("Failed to get hash for subdir/file2.txt for expected map: %v", err)}


		expectedMap := map[string]string{
			"file1.txt":                         hashOfFile1,
			filepath.Join("subdir", "file2.txt"): hashOfFile2,
		}
		t.Logf("Expected map: %+v", expectedMap)


		resultMap, err := ScanFolder(baseDir, configDirToSkip)
		if err != nil {
			t.Fatalf("ScanFolder() error = %v, wantErr nil", err)
		}
		t.Logf("Result map: %+v", resultMap)

		if !reflect.DeepEqual(expectedMap, resultMap) {
			// For better error reporting, print differences
			for k, vExp := range expectedMap {
				vRes, ok := resultMap[k]
				if !ok {
					t.Errorf("Expected key %s not found in result map", k)
				} else if vRes != vExp {
					t.Errorf("Value mismatch for key %s: got %s, want %s", k, vRes, vExp)
				}
			}
			for k := range resultMap {
				if _, ok := expectedMap[k]; !ok {
					t.Errorf("Unexpected key %s found in result map", k)
				}
			}
			t.Errorf("ScanFolder() resultMap = %+v, want %+v", resultMap, expectedMap)
		}
	})

	// Test 2: Empty directory
	t.Run("EmptyDirectory", func(t *testing.T) {
		emptyDir := t.TempDir()
		configDirToSkip := ".filesync"

		expectedEmptyMap := make(map[string]string) // Correctly initialized empty map

		resultMap, err := ScanFolder(emptyDir, configDirToSkip)
		if err != nil {
			t.Fatalf("ScanFolder() for empty directory error = %v, wantErr nil", err)
		}

		if len(resultMap) != 0 { // More explicit check than DeepEqual with potentially nil vs empty map
			t.Errorf("ScanFolder() for empty directory resultMap = %+v, want empty map", resultMap)
		}
		if !reflect.DeepEqual(expectedEmptyMap, resultMap) {
			 // This might fail if one is nil and other is empty map, though len check is good.
			t.Errorf("ScanFolder() for empty directory resultMap (DeepEqual) = %+v, want %+v", resultMap, expectedEmptyMap)
		}
	})

	// Test 3: Directory with only the .filesync folder (should be empty result)
	t.Run("OnlyDotFileSync", func(t *testing.T) {
		baseDir := t.TempDir()
		configDirToSkip := ".filesync"
		createFile(t, filepath.Join(baseDir, configDirToSkip, "someconfig.json"), "config_content")

		expectedEmptyMap := make(map[string]string)

		resultMap, err := ScanFolder(baseDir, configDirToSkip)
		if err != nil {
			t.Fatalf("ScanFolder() with only .filesync dir error = %v, wantErr nil", err)
		}
		// if len(resultMap) != 0 { // Using DeepEqual is more comprehensive
		// 	t.Errorf("ScanFolder() with only .filesync dir resultMap = %+v, want empty map", resultMap)
		// }
		if !reflect.DeepEqual(expectedEmptyMap, resultMap) {
			t.Errorf("ScanFolder() with only .filesync dir resultMap = %+v, want %+v", resultMap, expectedEmptyMap)
		}
	})

	// Test 4: Non-existent base directory
    t.Run("NonExistentBaseDir", func(t *testing.T) {
        // Using a hardcoded path that is extremely unlikely to exist.
        // No need for unused variables like nonExistentDir or removedDir here.
        _, err := ScanFolder("a_very_very_surely_non_existent_directory_for_scan_test_12345", ".filesync")
        if err == nil {
            t.Errorf("ScanFolder() with non-existent base dir expected an error, got nil")
        } else {
            t.Logf("ScanFolder() with non-existent base dir got expected error: %v", err)
        }
    })
}
