package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

type Recorder struct {
	recording bool
	Content   []byte
	done      chan struct{}
	finished  chan struct{}
	Stopped   time.Time
	mu        sync.RWMutex
}

func NewRecorder() *Recorder {
	return &Recorder{
		recording: false,
		done:      make(chan struct{}),
		finished:  make(chan struct{}),
	}
}

func (r *Recorder) IsRecording() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.recording
}

// recordAudio captures audio from the microphone until Ctrl+B is pressed
func (r *Recorder) Start() ([]byte, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(channels)
	deviceConfig.SampleRate = uint32(sampleRate)

	var capturedBytes []byte

	onRecvFrames := func(pOutputSample, pInputSamples []byte, framecount uint32) {
		r.mu.Lock()
		capturedBytes = append(capturedBytes, pInputSamples...)
		r.mu.Unlock()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: onRecvFrames,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize capture device: %w", err)
	}

	err = device.Start()
	if err != nil {
		device.Uninit()
		return nil, fmt.Errorf("failed to start capture device: %w", err)
	}

	r.mu.Lock()
	r.recording = true
	r.mu.Unlock()

	log.Println("Recording")

	// Wait until stopped
	<-r.done

	device.Stop()
	device.Uninit()

	// Convert raw PCM bytes to []int16
	r.mu.Lock()
	raw := capturedBytes
	r.mu.Unlock()

	allSamples := make([]int16, len(raw)/2)
	for i := range allSamples {
		allSamples[i] = int16(raw[2*i]) | int16(raw[2*i+1])<<8
	}

	// Convert to WAV format
	wavData := samplesToWAV(allSamples, sampleRate, channels)
	r.Content = wavData
	r.mu.Lock()
	r.recording = false
	r.mu.Unlock()

	r.Stopped = time.Now()
	close(r.finished)
	return wavData, nil
}

func (r *Recorder) Stop() {
	if !r.recording {
		return
	}
	r.done <- struct{}{}
	<-r.finished
	// Reset channels for next recording
	r.done = make(chan struct{})
	r.finished = make(chan struct{})
}

// samplesToWAV converts raw audio samples to WAV format
func samplesToWAV(samples []int16, sampleRate, channels int) []byte {
	var buf bytes.Buffer

	dataSize := len(samples) * 2 // 2 bytes per sample (16-bit)
	fileSize := wavHeaderSize + dataSize - 8

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, int32(fileSize))
	buf.WriteString("WAVE")

	// fmt subchunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, int32(16))         // Subchunk1Size (16 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(1))          // AudioFormat (1 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(channels))   // NumChannels
	binary.Write(&buf, binary.LittleEndian, int32(sampleRate)) // SampleRate
	byteRate := sampleRate * channels * 2                      // ByteRate
	binary.Write(&buf, binary.LittleEndian, int32(byteRate))
	blockAlign := channels * 2 // BlockAlign
	binary.Write(&buf, binary.LittleEndian, int16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, int16(16)) // BitsPerSample

	// data subchunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, int32(dataSize))

	// Write audio data
	for _, sample := range samples {
		binary.Write(&buf, binary.LittleEndian, sample)
	}

	return buf.Bytes()
}

// transcribeWithGroq sends audio to Groq API for transcription
func transcribeWithGroq(audioData []byte, apiKey string, language string) (string, error) {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add audio file
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = part.Write(audioData)
	if err != nil {
		return "", fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add model field
	err = writer.WriteField("model", "whisper-large-v3")
	if err != nil {
		return "", fmt.Errorf("failed to write model field: %w", err)
	}

	// Add Language field
	err = writer.WriteField("language", language)
	if err != nil {
		return "", fmt.Errorf("failed to write language field: %w", err)
	}

	// Add response format
	err = writer.WriteField("response_format", "json")
	if err != nil {
		return "", fmt.Errorf("failed to write response_format field: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", groqAudioAPIURL, &requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var transcriptionResp GroqTranscriptionResponse
	err = json.Unmarshal(body, &transcriptionResp)
	if err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return transcriptionResp.Text, nil
}
