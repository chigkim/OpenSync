package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os" // For os.ReadFile and os.Stat (if used for debug)
	// "io" // No longer needed for io.Copy
)

// GetFileHash calculates the SHA256 hash of a file.
// It returns the hex-encoded string of the hash.
func GetFileHash(filepath string) (string, error) {
	data, err := os.ReadFile(filepath) // Read all data at once
	if err != nil {
		return "", fmt.Errorf("failed to read file '%s': %w", filepath, err)
	}
	// For debugging, one could print len(data) here to stderr
	// fmt.Fprintf(os.Stderr, "DEBUG GetFileHash: Read %d bytes from file %s\n", len(data), filepath)


	hasher := sha256.New()
	_, err = hasher.Write(data) // Write data directly to hasher
	if err != nil {
		// This error is less likely with Write than io.Copy but good to have
		return "", fmt.Errorf("failed to write data to hasher for '%s': %w", filepath, err)
	}

	hashInBytes := hasher.Sum(nil)
	hashString := hex.EncodeToString(hashInBytes)

	return hashString, nil
}
