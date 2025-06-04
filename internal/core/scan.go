package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanFolder recursively scans the given folderPath and returns a map of
// relative file paths to their SHA256 hashes.
// It skips the directory specified by configDirName (e.g., ".filesync")
// at the root of folderPath.
func ScanFolder(folderPath string, configDirNameToSkip string) (map[string]string, error) {
	fileHashes := make(map[string]string)
	absFolderPath, err := filepath.Abs(folderPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for '%s': %w", folderPath, err)
	}

	// Check if the base folder exists before walking
	if _, statErr := os.Stat(absFolderPath); os.IsNotExist(statErr) {
		return nil, fmt.Errorf("base folder '%s' (resolved to '%s') does not exist: %w", folderPath, absFolderPath, statErr)
	} else if statErr != nil {
		return nil, fmt.Errorf("failed to stat base folder '%s' (resolved to '%s'): %w", folderPath, absFolderPath, statErr)
	}

	// Normalize configDirNameToSkip to ensure it's just the directory name
	configDirToSkipClean := filepath.Clean(configDirNameToSkip)
	if filepath.IsAbs(configDirToSkipClean) || strings.Contains(configDirToSkipClean, string(filepath.Separator)) {
		// This ensures configDirNameToSkip is a simple name, not a path segment.
		// For this implementation, we assume it's a single directory name like ".filesync".
		// More complex skip logic would require more robust path manipulation.
		return nil, fmt.Errorf("configDirNameToSkip '%s' must be a simple directory name, not a path", configDirNameToSkip)
	}

	walkErr := filepath.WalkDir(absFolderPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Error accessing path, could be permissions. Report it.
			// The caller of ScanFolder might decide how to handle this (e.g., log and continue, or abort).
			fmt.Fprintf(os.Stderr, "Warning: Error accessing path %q: %v. Skipping.\n", path, err)
			// To skip only this entry and continue walking, return nil.
			// To abort WalkDir, return the error.
			return nil // Continue walking if possible
		}

		// Get relative path
		relPath, err := filepath.Rel(absFolderPath, path)
		if err != nil {
			// This should ideally not happen if absFolderPath is a prefix of path
			return fmt.Errorf("failed to get relative path for '%s' based on '%s': %w", path, absFolderPath, err)
		}

		// Skip the root folder itself (relPath will be ".")
		if relPath == "." {
			return nil
		}

		// Check if the path is within the config directory at the root
		pathParts := strings.Split(filepath.ToSlash(relPath), "/") // Use ToSlash for consistent separator
		if len(pathParts) > 0 && pathParts[0] == configDirToSkipClean {
			if d.IsDir() {
				// If it's the config directory itself, skip its contents
				// fmt.Fprintf(os.Stderr, "Debug: Skipping config directory: %s (rel: %s)\n", path, relPath)
				return filepath.SkipDir
			}
			// If it's a file directly in a path segment named like configDirToSkipClean (e.g. a/b/.filesync),
			// this check pathParts[0] == configDirToSkipClean is only for root.
			// A more general skip would be strings.Contains(relPath, configDirToSkipClean)
			// but the requirement is to skip it at the root.
			// So, if pathParts[0] is the dir to skip, any file starting with it is skipped.
			// fmt.Fprintf(os.Stderr, "Debug: Skipping file/dir within root config dir: %s\n", relPath)
			return nil // Skip this file/dir
		}

		if !d.IsDir() {
			// It's a file, calculate its hash
			// fmt.Fprintf(os.Stderr, "Debug: Hashing file: %s (rel: %s)\n", path, relPath)
			hash, err := GetFileHash(path) // GetFileHash is in hash.go (same package 'core')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to hash file %q: %v. Skipping.\n", path, err)
				return nil // Skip this file, continue walking
			}
			fileHashes[relPath] = hash
		}
		return nil
	})

	if walkErr != nil {
		// This error is from the WalkDir function itself if an error was returned by the walkFn
		// that wasn't filepath.SkipDir.
		return nil, fmt.Errorf("error during directory walk of '%s': %w", absFolderPath, walkErr)
	}

	return fileHashes, nil
}
