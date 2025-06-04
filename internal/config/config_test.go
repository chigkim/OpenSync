package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigLoadSave(t *testing.T) {
	// Test 1: Save a config and load it back
	tempDir1 := t.TempDir()
	t.Logf("Test 1: Using temp directory: %s", tempDir1)

	sampleConfig := Config{
		Devices: map[string]DeviceConfig{
			"dev1": {Name: "dev1", Address: "192.168.1.10", Port: 1234, Key: "key1"},
			"dev2": {Name: "dev2", Address: "10.0.0.5", Port: 5678, Key: "key2"},
		},
	}

	err := SaveConfig(tempDir1, &sampleConfig)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v, wantErr nil", err)
	}

	// Verify the file was created
	expectedConfigFilePath := filepath.Join(tempDir1, configDirName, configFileName)
	if _, err := os.Stat(expectedConfigFilePath); os.IsNotExist(err) {
		t.Errorf("Config file '%s' was not created by SaveConfig", expectedConfigFilePath)
	}
	t.Logf("Config file '%s' found after SaveConfig.", expectedConfigFilePath)


	loadedConfig, err := LoadConfig(tempDir1)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, wantErr nil", err)
	}
	t.Logf("Loaded config from '%s': %+v", tempDir1, loadedConfig)


	if !reflect.DeepEqual(sampleConfig, *loadedConfig) {
		t.Errorf("Loaded config = %+v, want %+v", *loadedConfig, sampleConfig)
	}
	t.Logf("Save and Load test successful. Loaded config matches sample config.")

	// Test 2: Loading from a non-existent config path (should return default)
	tempDir2 := t.TempDir()
	t.Logf("Test 2: Using new temp directory for non-existent config: %s", tempDir2)

	defaultConfig, err := LoadConfig(tempDir2)
	if err != nil {
		// LoadConfig is designed to return a new config and no error if file simply doesn't exist.
		// An error here would be unexpected (e.g., issues with tempDir itself).
		t.Fatalf("LoadConfig() from new/empty directory error = %v, wantErr nil (or specific not-exist if that was the design)", err)
	}

	if defaultConfig == nil {
		t.Fatal("LoadConfig() from new/empty directory returned nil config, want non-nil default config.")
	}
	if defaultConfig.Devices == nil {
		t.Errorf("LoadConfig() from new/empty directory returned defaultConfig.Devices = nil, want non-nil empty map")
	}
	if len(defaultConfig.Devices) != 0 {
		t.Errorf("LoadConfig() from new/empty directory returned len(defaultConfig.Devices) = %d, want 0", len(defaultConfig.Devices))
	}
	t.Logf("LoadConfig from non-existent path test successful. Got default config: %+v", defaultConfig)

	// Test 3: LoadConfig with an empty config file
	tempDir3 := t.TempDir()
	t.Logf("Test 3: Using temp directory for empty config file: %s", tempDir3)
	emptyConfigDir := filepath.Join(tempDir3, configDirName)
	err = os.MkdirAll(emptyConfigDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create directory for empty config test: %v", err)
	}
	emptyConfigFilePath := filepath.Join(emptyConfigDir, configFileName)
	_, err = os.Create(emptyConfigFilePath)
	if err != nil {
		t.Fatalf("Failed to create empty config file for test: %v", err)
	}

	emptyLoadedConfig, err := LoadConfig(tempDir3)
	if err != nil {
		t.Fatalf("LoadConfig() with empty file error = %v", err)
	}
	if emptyLoadedConfig == nil || emptyLoadedConfig.Devices == nil || len(emptyLoadedConfig.Devices) != 0 {
		t.Errorf("LoadConfig() with empty file = %+v, want empty non-nil map", emptyLoadedConfig)
	}
	t.Logf("LoadConfig with empty file test successful.")
}
