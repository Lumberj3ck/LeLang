package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
)

// PiperTts, ElevenLabs
type TTSBackend struct {
	Type  string `json:"type"`
	Voice string `json:"voice"`
}

type Config struct {
	Language                  string     `json:"language"`
	TargetTranslationLanguage string     `json:"target_translation_language"`
	TTSBackend                TTSBackend `json:"tts_backend"`
	// whispercpp, hosted whispercpp
	STTBackend string `json:"stt_backend"`
}

func NewConfig() Config {
	return Config{
		Language:                  "de",
		TargetTranslationLanguage: "en",
		TTSBackend: TTSBackend{
			Type:  "piper",
			Voice: "de_DE-karlsson-low.onnx",
		},
		STTBackend: "hosted",
	}
}

func CreateDefaultConfig() (Config, error) {
	config := NewConfig()

	configPath := GetConfigPath()

	err := os.MkdirAll(filepath.Dir(configPath), 0755)
	if err != nil {
		return Config{}, err
	}

	file, err := os.Create(configPath)
	if err != nil {
		return	Config{}, err
	}

	defer file.Close()

	s, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Config{}, err
	}

	file.Write(s)
	return config, nil
}

func GetConfigPath() string {
	d, err := os.UserHomeDir()

	if err != nil {
		d = "."
	}
	return filepath.Join(d, ".config", "lazylang", "config.json")
}

func GetConfig() (Config, error) {
	configPath := GetConfigPath()
	configFile, err := os.Open(configPath)

	if errors.Is(err, os.ErrNotExist) {
		c, err := CreateDefaultConfig()
		if err != nil {
			log.Fatal(err)
		}
		return c, nil
	}

	if err != nil {
		return Config{}, err
	} 

	defer configFile.Close()

	byteValue, _ := io.ReadAll(configFile)
	var config Config

	err = json.Unmarshal(byteValue, &config)

	if err != nil {
		return Config{}, err
	}

	return config, nil
}
