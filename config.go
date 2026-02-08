package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lazylang/piper"
	"log"
	"log/slog"
	"net/http"
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
	LibreTranslateURL         string     `json:"libre_translate_url"`
	TTSBackend                TTSBackend `json:"tts_backend"`
	// whispercpp, hosted whispercpp
	STTBackend STTBackend `json:"stt_backend"`
}

type STTBackend struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

func NewConfig() Config {
	return Config{
		Language:                  "de",
		TargetTranslationLanguage: "en",
		LibreTranslateURL:         "http://localhost:5000",
		TTSBackend: TTSBackend{
			Type:  "piper",
			Voice: "de_DE-karlsson-low.onnx",
		},
		STTBackend: STTBackend{
			Type:  "hosted",
			Model: "whisper-large-v3",
		},
	}
}

func CreateDefaultConfig() (Config, error) {
	config := NewConfig()

	configPath := GetConfigPath()

	err := os.MkdirAll(filepath.Dir(configPath), 0755)
	if err != nil {
		return config, err
	}

	file, err := os.Create(configPath)
	if err != nil {
		return config, err
	}

	defer file.Close()

	s, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return config, err
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

var invalidApiKey = errors.New("Invalid API key")

func isValid(config Config, apiKey string) error {
	model := config.STTBackend.Model
	client := &http.Client{}

	url := fmt.Sprintf("%v/models/%v", groqAPIBaseURL, model)
	req, err := http.NewRequest("GET", url , nil)

	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	log.Println(resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return invalidApiKey
	default:
		return errors.New("Invalid model")
	}
}

func resolvePiperVoice(language string, defaultConfig Config) (string, string) {
	voices, err := piper.FetchVoices()
	if err != nil {
		slog.Error("Failed to fetch voices; Defaulting to de_DE-karlsson-low.onnx", "error", err)
		return defaultConfig.TTSBackend.Voice, defaultConfig.Language
	}
	var v string
	for _, voice := range voices {
		if voice.Language.Family == language {
			v = voice.Key
		}
	}

	if v == "" {
		slog.Error("Language not found in voices; Defaulting to de_DE-karlsson-low.onnx", "language", language)
		return defaultConfig.TTSBackend.Voice, defaultConfig.Language
	}
	return v + ".onnx", language
}

func populateDefaults(config Config) Config {
	defaultConfig := NewConfig()
	if config.LibreTranslateURL == "" {
		config.LibreTranslateURL = defaultConfig.LibreTranslateURL
	}

	if config.TTSBackend.Type == "" {
		config.TTSBackend.Type = defaultConfig.TTSBackend.Type
	}

	if config.TTSBackend.Type == "piper" && config.TTSBackend.Voice == "" {
		voice, language := resolvePiperVoice(config.Language, defaultConfig)
		config.TTSBackend.Voice = voice
		config.Language = language
	}

	return config
}

func GetConfig(apiKey string) (Config, error) {
	configPath := GetConfigPath()
	configFile, err := os.Open(configPath)

	if errors.Is(err, os.ErrNotExist) {
		c, err := CreateDefaultConfig()
		if err != nil {
			return NewConfig(), err
		}
		return c, nil
	}

	if err != nil {
		return NewConfig(), err
	}

	defer configFile.Close()

	byteValue, _ := io.ReadAll(configFile)
	var config Config

	err = json.Unmarshal(byteValue, &config)

	if err != nil {
		return NewConfig(), err
	}

	err = isValid(config, apiKey)
	if err != nil {
		return NewConfig(), err
	}

	config = populateDefaults(config)
	return config, nil
}
