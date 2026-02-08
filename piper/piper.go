package piper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
	"golang.org/x/text/unicode/norm"
)

var home, _ = os.UserHomeDir()
var voicesDir = filepath.Join(home, ".piper-voices")

const voicesURL = "https://huggingface.co/rhasspy/piper-voices/resolve/main/voices.json"
const baseDownloadURL = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0"

// VoiceInfo represents metadata about a Piper voice
type VoiceInfo struct {
	Key       string               `json:"key"`
	Name      string               `json:"name"`
	Language  VoiceLanguage        `json:"language"`
	Quality   string               `json:"quality"`
	NumSpkrs  int                  `json:"num_speakers"`
	SpeakerID map[string]int       `json:"speaker_id_map,omitempty"`
	Files     map[string]VoiceFile `json:"files"`
}

type VoiceLanguage struct {
	Code           string `json:"code"`
	Family         string `json:"family"`
	Region         string `json:"region"`
	NameNative     string `json:"name_native"`
	NameEnglish    string `json:"name_english"`
	CountryEnglish string `json:"country_english"`
}

type VoiceFile struct {
	SizeBytes int64  `json:"size_bytes"`
	MD5Digest string `json:"md5_digest"`
}

// cachedVoices holds the downloaded voices.json data
var cachedVoices map[string]VoiceInfo

func MarshalVoices(body []byte) (map[string]VoiceInfo, error) {
	var voices map[string]VoiceInfo
	if err := json.Unmarshal(body, &voices); err != nil {
		return nil, fmt.Errorf("failed to parse voices.json: %w", err)
	}
	return voices, nil
}

// FetchVoices downloads and caches the voices.json file
func FetchVoices() (map[string]VoiceInfo, error) {
	voicesFile := filepath.Join(voicesDir, "voices.json")
	if cachedVoices != nil {
		return cachedVoices, nil
	} else if _, err := os.Stat(voicesFile); err == nil {
		buff, err := os.ReadFile(filepath.Join(voicesDir, "voices.json"))

		if err == nil {
			voices, err := MarshalVoices(buff)
			if err == nil {
				cachedVoices = voices
				return voices, nil
			}
		}
	}

	resp, err := http.Get(voicesURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch voices.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch voices.json: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read voices.json: %w", err)
	}

	voices, err := MarshalVoices(body)
	if err != nil {
		return nil, err
	}

	cachedVoices = voices
	return voices, nil
}

func saveToFile(data []byte, filename string) error {
	err := os.MkdirAll(voicesDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create voices directory: %w", err)
	}

	filePath := filepath.Join(voicesDir, filename)
	err = os.WriteFile(filePath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", filePath, err)
	}

	return nil
}

// ListLanguages prints all available languages for Piper TTS
func ListLanguages() error {
	voices, err := FetchVoices()
	if err != nil {
		return err
	}

	// Collect unique languages
	languages := make(map[string]VoiceLanguage)
	for _, voice := range voices {
		langCode := voice.Language.Code
		if _, exists := languages[langCode]; !exists {
			languages[langCode] = voice.Language
		}
	}

	// Sort by language code
	codes := make([]string, 0, len(languages))
	for code := range languages {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	fmt.Printf("Available languages (%d):\n", len(codes))
	fmt.Println(strings.Repeat("-", 50))
	for _, code := range codes {
		lang := languages[code]
		fmt.Printf("  %-10s %s (%s)\n", code, lang.NameEnglish, lang.NameNative)
	}

	return nil
}

// ListVoices prints all available voices for a specific language
func ListVoices(language string) error {
	voices, err := FetchVoices()
	if err != nil {
		return err
	}

	// Filter voices by language
	var matchingVoices []VoiceInfo
	for _, voice := range voices {
		// Match by language code (e.g., "en_US", "en_GB", or just "en")
		if voice.Language.Code == language ||
			strings.HasPrefix(voice.Language.Code, language+"_") ||
			strings.Split(voice.Language.Code, "_")[0] == language {
			matchingVoices = append(matchingVoices, voice)
		}
	}

	if len(matchingVoices) == 0 {
		return fmt.Errorf("no voices found for language: %s", language)
	}

	// Sort by key
	sort.Slice(matchingVoices, func(i, j int) bool {
		return matchingVoices[i].Key < matchingVoices[j].Key
	})

	fmt.Printf("Available voices for '%s' (%d):\n", language, len(matchingVoices))
	fmt.Println(strings.Repeat("-", 70))
	for _, voice := range matchingVoices {
		speakers := ""
		if voice.NumSpkrs > 1 {
			speakers = fmt.Sprintf(" [%d speakers]", voice.NumSpkrs)
		}
		fmt.Printf("  %-40s %-10s %s%s\n", voice.Key, voice.Quality, voice.Language.Code, speakers)
	}

	return nil
}

// DownloadVoice downloads a voice model and its config file
func DownloadVoice(language string, voice string) error {
	voices, err := FetchVoices()
	if err != nil {
		return err
	}

	// Build the voice key to look up
	voiceKey := strings.TrimRight(voice, ".onnx")

	voiceInfo, exists := voices[voiceKey]
	if !exists {
		// Try finding a partial match
		for key, v := range voices {
			if strings.Contains(key, voice) && strings.HasPrefix(key, language) {
				voiceKey = key
				voiceInfo = v
				exists = true
				break
			}
		}
	}

	if !exists {
		return fmt.Errorf("voice not found: %s (try ListVoices to see available voices)", voiceKey)
	}

	// Create voices directory
	if err := os.MkdirAll(voicesDir, 0755); err != nil {
		return fmt.Errorf("failed to create voices directory: %w", err)
	}

	// Download each file associated with the voice
	for filename := range voiceInfo.Files {
		// Build download URL based on voice key structure
		// Voice keys are like "en_US-lessac-medium", files are like "en_US-lessac-medium.onnx"
		downloadURL := fmt.Sprintf("%s/%s", baseDownloadURL, filename)
		log.Println("Downloading", downloadURL)

		resp, err := http.Get(downloadURL)
		if err != nil {
			return fmt.Errorf("failed to download %s: %w", filename, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to download %s: status %d", filename, resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}

		// Extract just the filename from the path
		localFilename := filepath.Base(filename)
		if err := saveToFile(data, localFilename); err != nil {
			return err
		}
	}

	return nil
}

type PiperVoice struct {
	Language string
	Model    string
	speaking bool
	mu       sync.RWMutex
}

type PiperOption func(*PiperVoice)

func WithLanguage(language string) PiperOption {
	return func(pv *PiperVoice) {
		pv.Language = language
	}
}

func WithModel(model string) PiperOption {
	return func(pv *PiperVoice) {
		pv.Model = model
	}
}

func NewPiperVoice(options ...PiperOption) *PiperVoice {
	pv := PiperVoice{
		Language: "de",
		Model:    "de_DE-karlsson-low.onnx",
	}

	for _, option := range options {
		option(&pv)
	}
	return &pv
}

type ErrorModelNotFound struct {
	Model    string
	Language string
}

func (e ErrorModelNotFound) Error() string {
	return fmt.Sprintf("Model %s not found for language %s", e.Model, e.Language)
}

type StoppedSpeaking struct {
}

func (e StoppedSpeaking) Error() string {
	return "Stopped speaking"
}

func (p *PiperVoice) IsSpeaking() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.speaking
}

// speakWithPiper generates speech using Piper TTS and plays it
func (p *PiperVoice) Speak(piper_ctx context.Context, text string) error {
	p.mu.Lock()
	p.speaking = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.speaking = false
		p.mu.Unlock()
	}()
	err := func() error {
		modelFile := filepath.Join(voicesDir, p.Model)
		_, err := os.Stat(modelFile)

		slog.Debug("Searching for", "modelFile", modelFile)
		if err != nil {
			return ErrorModelNotFound{Model: p.Model, Language: p.Language}
		}

		// Create piper command
		// Piper reads from stdin and outputs WAV to stdout
		piperCmd := exec.CommandContext(piper_ctx, "piper-tts", "--model", modelFile, "--output_raw")

		text = strings.ReplaceAll(text, "\n", " ")
		text = norm.NFC.String(text)
		piperCmd.Stdin = bytes.NewBufferString(text)

		// Connect piper stdout to paplay stdin
		pipe, err := piperCmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create pipe: %w", err)
		}

		// Capture stderr for debugging
		var piperStderr bytes.Buffer
		piperCmd.Stderr = &piperStderr

		ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
			// log.Printf("LOG <%v>\n", message)
		})
		if err != nil {
			return err
		}
		defer func() {
			_ = ctx.Uninit()
			ctx.Free()
		}()

		deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
		deviceConfig.Playback.Format = malgo.FormatS16
		deviceConfig.Playback.Channels = 1
		deviceConfig.SampleRate = 22050
		deviceConfig.Alsa.NoMMap = 1

		reader := bufio.NewReaderSize(pipe, 64*1024)
		eofReached := atomic.Bool{}
		playbackDone := make(chan struct{})
		silenceCallbacks := atomic.Int32{}
		onSamples := func(pOutputSample, pInputSamples []byte, framecount uint32) {
			select {
			case <-piper_ctx.Done():
				return
			default:
				if eofReached.Load() {
					for i := range pOutputSample {
						pOutputSample[i] = 0
					}
					// After a few silence callbacks, signal that playback is truly done
					if silenceCallbacks.Add(1) >= 4 {
						select {
						case playbackDone <- struct{}{}:
						default:
						}
					}
					return
				}
				n, err := io.ReadFull(reader, pOutputSample)
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					eofReached.Store(true)
					for i := n; i < len(pOutputSample); i++ {
						pOutputSample[i] = 0
					}
					return
				}
				if err != nil {
					slog.Info("Read error", "error", err)
					eofReached.Store(true)
					for i := range pOutputSample {
						pOutputSample[i] = 0
					}
					return
				}
			}

		}

		deviceCallbacks := malgo.DeviceCallbacks{
			Data: onSamples,
		}

		device, err := malgo.InitDevice(ctx.Context, deviceConfig, deviceCallbacks)
		if err != nil {
			return err
		}
		defer device.Uninit()

		go func() {
			err = device.Start()
			if err != nil {
				slog.Error("failed to start device:", "error", err)
			}
		}()
		defer device.Stop()

		// Start piper
		err = piperCmd.Start()
		if err != nil {
			return fmt.Errorf("failed to start piper: %w", err)
		}

		// Wait for playback to actually finish (silence callbacks confirm device drained)
		// IMPORTANT: piperCmd.Wait() must be called AFTER all reads from the pipe complete,
		// because Wait() closes the pipe and discards any unread data in the OS buffer.
		select {
		case <-piper_ctx.Done():
			return nil
		case <-playbackDone:
		}

		piperErr := piperCmd.Wait()
		if piperErr != nil && piper_ctx.Err() != context.Canceled {
			return piperErr
		}

		log.Printf("Speaking: %s", text)
		return nil
	}()

	return err
}
